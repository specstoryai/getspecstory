import XCTest
@testable import SpecStoryKit

final class ConfigTOMLTests: XCTestCase {
    private var tempDir: URL!

    override func setUpWithError() throws {
        tempDir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("configtoml-tests-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: tempDir)
    }

    private func writeConfig(_ text: String, name: String = "config.toml") throws -> URL {
        let url = tempDir.appendingPathComponent(name)
        try text.write(to: url, atomically: true, encoding: .utf8)
        return url
    }

    private func readBack(_ url: URL) throws -> String {
        try String(contentsOf: url, encoding: .utf8)
    }

    // MARK: - URL derivation

    func testConfigURLs() {
        XCTAssertTrue(ConfigTOML.userConfigURL.path.hasSuffix("/.specstory/cli/config.toml"))
        XCTAssertEqual(
            ConfigTOML.projectConfigURL(projectPath: "/Users/dev/proj").path,
            "/Users/dev/proj/.specstory/cli/config.toml"
        )
    }

    // MARK: - Reading

    func testReadQuotedValueWithCommentsAndWhitespace() throws {
        let url = try writeConfig("""
        # SpecStory CLI configuration
        [cloud_sync]
        enabled = true

        [local_sync]
        # where markdown goes
          output_dir   =   "~/notes/specstory"   # trailing comment
        """)
        XCTAssertEqual(ConfigTOML.outputDir(configURL: url), "~/notes/specstory")
    }

    func testReadSingleQuotedAndBareValues() throws {
        let single = try writeConfig("[local_sync]\noutput_dir = '~/single'\n", name: "single.toml")
        XCTAssertEqual(ConfigTOML.outputDir(configURL: single), "~/single")

        let bare = try writeConfig("[local_sync]\noutput_dir = ~/bare # comment\n", name: "bare.toml")
        XCTAssertEqual(ConfigTOML.outputDir(configURL: bare), "~/bare")
    }

    func testReadIgnoresKeyOutsideLocalSync() throws {
        let url = try writeConfig("""
        [other]
        output_dir = "/wrong"

        [local_sync]
        output_dir = "/right"
        """)
        XCTAssertEqual(ConfigTOML.outputDir(configURL: url), "/right")
    }

    func testReadMissingFileOrKeyIsNil() throws {
        XCTAssertNil(ConfigTOML.outputDir(configURL: tempDir.appendingPathComponent("absent.toml")))
        let url = try writeConfig("[local_sync]\nenabled = true\n")
        XCTAssertNil(ConfigTOML.outputDir(configURL: url))
    }

    // MARK: - Updating

    func testUpdateExistingKeyPreservesEverythingElse() throws {
        let url = try writeConfig("""
        # header comment
        [cloud_sync]
        enabled = true

        [local_sync]
        # keep this comment
        output_dir = "/old/place"
        other_key = 7
        """)
        try ConfigTOML.setOutputDir("~/new/place", configURL: url)
        let text = try readBack(url)
        XCTAssertTrue(text.contains("# header comment"))
        XCTAssertTrue(text.contains("[cloud_sync]"))
        XCTAssertTrue(text.contains("enabled = true"))
        XCTAssertTrue(text.contains("# keep this comment"))
        XCTAssertTrue(text.contains("other_key = 7"))
        XCTAssertTrue(text.contains("output_dir = \"~/new/place\""))
        XCTAssertFalse(text.contains("/old/place"))
        XCTAssertEqual(ConfigTOML.outputDir(configURL: url), "~/new/place")
    }

    func testInsertIntoExistingSection() throws {
        let url = try writeConfig("""
        [local_sync]
        enabled = true
        """)
        try ConfigTOML.setOutputDir("/data/out", configURL: url)
        let text = try readBack(url)
        XCTAssertTrue(text.contains("[local_sync]\noutput_dir = \"/data/out\""))
        XCTAssertTrue(text.contains("enabled = true"))
        XCTAssertEqual(ConfigTOML.outputDir(configURL: url), "/data/out")
    }

    func testAppendNewSectionWhenAbsent() throws {
        let url = try writeConfig("""
        [cloud_sync]
        enabled = false
        """)
        try ConfigTOML.setOutputDir("~/appended", configURL: url)
        let text = try readBack(url)
        XCTAssertTrue(text.contains("[cloud_sync]"))
        XCTAssertTrue(text.contains("enabled = false"))
        XCTAssertTrue(text.hasSuffix("[local_sync]\noutput_dir = \"~/appended\"\n"))
        XCTAssertEqual(ConfigTOML.outputDir(configURL: url), "~/appended")
    }

    func testCreatesParentDirsAndFileWhenAbsent() throws {
        let url = tempDir.appendingPathComponent("deep/.specstory/cli/config.toml")
        try ConfigTOML.setOutputDir("~/fresh", configURL: url)
        XCTAssertEqual(ConfigTOML.outputDir(configURL: url), "~/fresh")
        XCTAssertEqual(try readBack(url), "[local_sync]\noutput_dir = \"~/fresh\"\n")
    }

    func testTildeKeptLiteral() throws {
        let url = tempDir.appendingPathComponent("tilde.toml")
        try ConfigTOML.setOutputDir("~/kept/literal", configURL: url)
        XCTAssertTrue(try readBack(url).contains("output_dir = \"~/kept/literal\""))
        XCTAssertEqual(ConfigTOML.outputDir(configURL: url), "~/kept/literal")
    }

    // MARK: - Removing

    func testRemoveKeyRemovesEmptySection() throws {
        let url = try writeConfig("""
        [cloud_sync]
        enabled = true

        [local_sync]
        output_dir = "/gone"
        """)
        try ConfigTOML.setOutputDir(nil, configURL: url)
        let text = try readBack(url)
        XCTAssertFalse(text.contains("output_dir"))
        XCTAssertFalse(text.contains("[local_sync]"))
        XCTAssertTrue(text.contains("[cloud_sync]"))
        XCTAssertTrue(text.contains("enabled = true"))
    }

    func testRemoveKeyKeepsSectionWithOtherKeys() throws {
        let url = try writeConfig("""
        [local_sync]
        enabled = true
        output_dir = "/gone"
        """)
        try ConfigTOML.setOutputDir(nil, configURL: url)
        let text = try readBack(url)
        XCTAssertFalse(text.contains("output_dir"))
        XCTAssertTrue(text.contains("[local_sync]"))
        XCTAssertTrue(text.contains("enabled = true"))
    }

    func testRemoveKeyKeepsSectionWithComments() throws {
        let url = try writeConfig("""
        [local_sync]
        # a comment worth keeping
        output_dir = "/gone"
        """)
        try ConfigTOML.setOutputDir(nil, configURL: url)
        let text = try readBack(url)
        XCTAssertFalse(text.contains("output_dir"))
        XCTAssertTrue(text.contains("[local_sync]"))
        XCTAssertTrue(text.contains("# a comment worth keeping"))
    }

    func testRemoveFromMissingFileIsNoOp() throws {
        let url = tempDir.appendingPathComponent("never-created.toml")
        try ConfigTOML.setOutputDir(nil, configURL: url)
        XCTAssertFalse(FileManager.default.fileExists(atPath: url.path))
    }

    func testRemoveMissingKeyLeavesFileUntouched() throws {
        let original = "[local_sync]\nenabled = true\n"
        let url = try writeConfig(original)
        try ConfigTOML.setOutputDir(nil, configURL: url)
        XCTAssertEqual(try readBack(url), original)
    }
}
