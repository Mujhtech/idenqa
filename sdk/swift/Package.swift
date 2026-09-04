// swift-tools-version: 6.2

import PackageDescription

let package = Package(
    name: "Idenqa",
    platforms: [.iOS(.v16), .macOS(.v13)],
    products: [.library(name: "Idenqa", targets: ["Idenqa"])],
    targets: [
        .target(name: "Idenqa"),
        .testTarget(name: "IdenqaTests", dependencies: ["Idenqa"]),
    ]
)
