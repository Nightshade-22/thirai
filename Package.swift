// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "Thirai",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "thirai",
            path: "Sources/thirai",
            resources: [.copy("web")],
            linkerSettings: [.linkedFramework("AppKit")]
        ),
    ]
)
