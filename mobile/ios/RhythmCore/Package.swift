// swift-tools-version: 6.0
import PackageDescription
let package = Package(name: "RhythmCore", platforms: [.iOS(.v17), .macOS(.v14)], products: [.library(name: "RhythmCore", targets: ["RhythmCore"])], targets: [.target(name: "RhythmCore"), .testTarget(name: "RhythmCoreTests", dependencies: ["RhythmCore"])])
