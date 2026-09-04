import Foundation

public struct AcquisitionCoordinator: Sendable {
    private let source: any RawCaptureSource
    private let assessor: any CaptureQualityAssessor
    private let presenter: any ChallengePresenter

    public init(source: any RawCaptureSource, assessor: any CaptureQualityAssessor, presenter: any ChallengePresenter) {
        self.source = source
        self.assessor = assessor
        self.presenter = presenter
    }

    public func acquire(_ requirement: CaptureRequirement) async throws -> [AcquiredFrame] {
        guard !requirement.id.isEmpty, !requirement.evidenceType.isEmpty, !requirement.artefact.isEmpty,
              requirement.quality.minimumWidth > 0, requirement.quality.minimumHeight > 0,
              requirement.quality.maximumBytes > 0, requirement.challenges.count <= 8 else {
            throw IdenqaError.invalidConfiguration
        }
        let challenges = requirement.challenges.isEmpty
            ? [LivenessChallenge(id: "capture", prompt: .neutral, maximumDuration: .seconds(15))]
            : requirement.challenges
        var frames: [AcquiredFrame] = []
        frames.reserveCapacity(challenges.count)
        for challenge in challenges {
            guard !challenge.id.isEmpty,
                  challenge.maximumDuration >= .milliseconds(500),
                  challenge.maximumDuration <= .seconds(15) else {
                throw IdenqaError.invalidConfiguration
            }
            let artifact = try await capture(challenge)
            guard artifact.acquisitionMethod == "idenqa.method.live_camera" else {
                throw IdenqaError.invalidResponse
            }
            let quality = try await assessor.assess(artifact)
            guard quality.failures(against: requirement.quality).isEmpty else {
                throw IdenqaError.captureQuality
            }
            frames.append(AcquiredFrame(
                challengeID: requirement.challenges.isEmpty ? nil : challenge.id,
                artifact: artifact,
                quality: quality
            ))
        }
        return frames
    }

    private func capture(_ challenge: LivenessChallenge) async throws -> CapturedArtifact {
        try await withThrowingTaskGroup(of: CapturedArtifact.self) { group in
            group.addTask {
                try await presenter.present(challenge)
                return try await source.capture()
            }
            group.addTask {
                try await Task.sleep(for: challenge.maximumDuration)
                throw IdenqaError.captureTimeout
            }
            defer { group.cancelAll() }
            guard let artifact = try await group.next() else { throw IdenqaError.invalidResponse }
            return artifact
        }
    }
}

#if canImport(CoreGraphics) && canImport(ImageIO) && canImport(Vision)
import CoreGraphics
import ImageIO
@preconcurrency import Vision

public struct AppleImageQualityAssessor: CaptureQualityAssessor {
    public init() {}

    public func assess(_ artifact: CapturedArtifact) async throws -> CaptureQualityMeasurement {
        guard artifact.contentType == "image/jpeg",
              let source = CGImageSourceCreateWithData(artifact.bytes as CFData, nil),
              let image = CGImageSourceCreateImageAtIndex(source, 0, nil) else {
            throw IdenqaError.invalidResponse
        }
        let samples = Self.samples(image)
        guard !samples.isEmpty else { throw IdenqaError.invalidResponse }
        let mean = samples.reduce(0, +) / Double(samples.count)
        let variance = samples.reduce(0) { $0 + (($1 - mean) * ($1 - mean)) } / Double(samples.count)
        let contrast = min(1, sqrt(variance) * 2)
        var adjacent = 0.0
        for index in 1..<samples.count { adjacent += abs(samples[index] - samples[index - 1]) }
        let sharpness = min(1, adjacent / Double(max(1, samples.count - 1)) * 4)
        let glare = Double(samples.filter { $0 >= 0.98 }.count) / Double(samples.count)

        let request = VNDetectFaceRectanglesRequest()
        try VNImageRequestHandler(cgImage: image).perform([request])
        return CaptureQualityMeasurement(
            width: image.width,
            height: image.height,
            byteCount: artifact.bytes.count,
            brightness: mean,
            contrast: contrast,
            sharpness: sharpness,
            glare: glare,
            faceCount: request.results?.count ?? 0
        )
    }

    private static func samples(_ image: CGImage) -> [Double] {
        let width = 64
        let height = 64
        var bytes = [UInt8](repeating: 0, count: width * height * 4)
        guard let context = CGContext(
            data: &bytes,
            width: width,
            height: height,
            bitsPerComponent: 8,
            bytesPerRow: width * 4,
            space: CGColorSpaceCreateDeviceRGB(),
            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
        ) else { return [] }
        context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
        var samples: [Double] = []
        samples.reserveCapacity(width * height)
        for index in stride(from: 0, to: bytes.count, by: 4) {
            let red = 0.2126 * Double(bytes[index])
            let green = 0.7152 * Double(bytes[index + 1])
            let blue = 0.0722 * Double(bytes[index + 2])
            samples.append((red + green + blue) / 255)
        }
        return samples
    }
}
#endif
