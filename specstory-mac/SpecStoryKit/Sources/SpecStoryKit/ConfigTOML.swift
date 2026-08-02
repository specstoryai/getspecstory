import Foundation

/// Minimal, line-based reader and editor for the specstory CLI config.toml.
/// Only the [local_sync] output_dir key is understood; every other line
/// (sections, keys, comments, blank lines) passes through byte-for-byte on
/// edit. Values are written as output_dir = "<dir>" with ~ kept literal:
/// the CLI expands it, the app never does.
public enum ConfigTOML {
    static let sectionName = "local_sync"
    static let keyName = "output_dir"

    public static var userConfigURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".specstory/cli/config.toml")
    }

    public static func projectConfigURL(projectPath: String) -> URL {
        URL(fileURLWithPath: projectPath)
            .appendingPathComponent(".specstory/cli/config.toml")
    }

    public static func outputDir(configURL: URL) -> String? {
        guard let text = try? String(contentsOf: configURL, encoding: .utf8) else { return nil }
        var inLocalSync = false
        for rawLine in text.components(separatedBy: "\n") {
            let line = rawLine.trimmingCharacters(in: .whitespaces)
            if let section = sectionHeader(line) {
                inLocalSync = section == sectionName
                continue
            }
            guard inLocalSync, let value = keyValue(line) else { continue }
            return value
        }
        return nil
    }

    public static func setOutputDir(_ dir: String?, configURL: URL) throws {
        let existingText = try? String(contentsOf: configURL, encoding: .utf8)

        // Removing from a file that does not exist is a no-op, not a create.
        if existingText == nil && dir == nil { return }

        var lines = (existingText ?? "").components(separatedBy: "\n")
        // A trailing newline shows up as one empty trailing element; drop it
        // while editing and restore it on write.
        if let last = lines.last, last.isEmpty { lines.removeLast() }
        if lines == [""] { lines = [] }

        let section = sectionRange(in: lines)
        let keyIndex = section.flatMap { range in
            lines[range].firstIndex { keyValue($0.trimmingCharacters(in: .whitespaces)) != nil }
        }

        if let dir {
            let keyLine = "\(keyName) = \"\(escaped(dir))\""
            if let keyIndex {
                lines[keyIndex] = keyLine
            } else if let section {
                lines.insert(keyLine, at: section.lowerBound + 1)
            } else {
                if !lines.isEmpty, lines.last?.trimmingCharacters(in: .whitespaces).isEmpty == false {
                    lines.append("")
                }
                lines.append("[\(sectionName)]")
                lines.append(keyLine)
            }
        } else {
            guard let section, let keyIndex else { return }
            lines.remove(at: keyIndex)
            // Re-measure the section, then drop it when nothing but blank
            // lines remain. Comments keep the section alive on purpose.
            if let remaining = sectionRange(in: lines) {
                let body = lines[(remaining.lowerBound + 1)..<remaining.upperBound]
                if body.allSatisfy({ $0.trimmingCharacters(in: .whitespaces).isEmpty }) {
                    lines.removeSubrange(remaining)
                }
            }
        }

        var output = lines.joined(separator: "\n")
        if !output.isEmpty { output += "\n" }

        try FileManager.default.createDirectory(
            at: configURL.deletingLastPathComponent(),
            withIntermediateDirectories: true
        )
        try output.write(to: configURL, atomically: true, encoding: .utf8)
    }

    // MARK: - Line parsing

    /// The [local_sync] header index up to (not including) the next section
    /// header or end of file.
    private static func sectionRange(in lines: [String]) -> Range<Int>? {
        var start: Int?
        for (index, rawLine) in lines.enumerated() {
            let line = rawLine.trimmingCharacters(in: .whitespaces)
            guard let section = sectionHeader(line) else { continue }
            if let start {
                return start..<index
            }
            if section == sectionName {
                start = index
            }
        }
        guard let start else { return nil }
        return start..<lines.count
    }

    /// "[local_sync]" -> "local_sync"; tolerates inner whitespace and
    /// trailing comments. Not a section header -> nil.
    static func sectionHeader(_ trimmedLine: String) -> String? {
        guard trimmedLine.hasPrefix("["),
              let close = trimmedLine.firstIndex(of: "]") else { return nil }
        let inner = trimmedLine[trimmedLine.index(after: trimmedLine.startIndex)..<close]
        return inner.trimmingCharacters(in: .whitespaces)
    }

    /// "output_dir = \"~/foo\"  # comment" -> "~/foo". Tolerates single
    /// quotes, double quotes, bare values, and surrounding whitespace.
    /// Not the output_dir key (or a comment line) -> nil.
    static func keyValue(_ trimmedLine: String) -> String? {
        guard !trimmedLine.hasPrefix("#") else { return nil }
        guard let equals = trimmedLine.firstIndex(of: "=") else { return nil }
        let key = trimmedLine[..<equals].trimmingCharacters(in: .whitespaces)
        guard key == keyName else { return nil }
        let rawValue = trimmedLine[trimmedLine.index(after: equals)...]
            .trimmingCharacters(in: .whitespaces)
        return parseValue(rawValue)
    }

    private static func parseValue(_ raw: String) -> String? {
        if raw.hasPrefix("\"") {
            return quotedValue(raw, quote: "\"", unescape: true)
        }
        if raw.hasPrefix("'") {
            return quotedValue(raw, quote: "'", unescape: false)
        }
        // Bare value: everything up to a comment marker.
        let bare = raw.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false)[0]
            .trimmingCharacters(in: .whitespaces)
        return bare.isEmpty ? nil : bare
    }

    private static func quotedValue(_ raw: String, quote: Character, unescape: Bool) -> String? {
        var value = ""
        var escaped = false
        for character in raw.dropFirst() {
            if unescape && escaped {
                value.append(character)
                escaped = false
                continue
            }
            if unescape && character == "\\" {
                escaped = true
                continue
            }
            if character == quote {
                return value
            }
            value.append(character)
        }
        return nil
    }

    private static func escaped(_ value: String) -> String {
        value
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
    }
}
