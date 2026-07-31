// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "SpecStoryKit",
    platforms: [.macOS(.v14)],
    products: [
        .library(name: "SpecStoryKit", targets: ["SpecStoryKit"])
    ],
    targets: [
        .target(name: "SpecStoryKit"),
        .testTarget(name: "SpecStoryKitTests", dependencies: ["SpecStoryKit"]),
    ]
)
