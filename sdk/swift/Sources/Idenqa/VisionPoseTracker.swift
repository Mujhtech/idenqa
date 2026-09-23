#if canImport(Vision) && canImport(ImageIO)
import Foundation
import ImageIO
@preconcurrency import Vision

/// Apple-first-party, local-only pose adapter for iOS 16+. Eye aperture is an
/// evaluation heuristic, not a blink classifier or a PAD result. Device-level
/// calibration of aperture and angle conventions remains required.
public actor VisionPoseTracker: CapturePoseTracker {
    public init() {}

    public func measure(_ artifact: CapturedArtifact) async throws -> CaptureFacePose {
        try Task.checkCancellation()
        guard artifact.contentType == "image/jpeg", artifact.bytes.count <= 20_971_520,
              let source = CGImageSourceCreateWithData(artifact.bytes as CFData, nil),
              let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [
                kCGImageSourceCreateThumbnailFromImageAlways: true,
                kCGImageSourceCreateThumbnailWithTransform: true,
                kCGImageSourceThumbnailMaxPixelSize: 1280
              ] as CFDictionary) else { throw IdenqaError.invalidResponse }
        // Legacy request deliberately retained for the selected iOS 16 floor.
        let request = VNDetectFaceLandmarksRequest()
        request.revision = VNDetectFaceLandmarksRequestRevision3
        try VNImageRequestHandler(cgImage: image).perform([request])
        try Task.checkCancellation()
        let faces = request.results ?? []
        guard let face = faces.first, face.confidence >= 0.7 else {
            return CaptureFacePose(faceCount: 0, yaw: .nan, pitch: .nan, roll: .nan,
                centerX: 0, centerY: 0, width: 0, height: 0, leftEyeClosed: .nan, rightEyeClosed: .nan)
        }
        let bounds = face.boundingBox
        let degrees = 180 / Double.pi
        return CaptureFacePose(faceCount: faces.count,
            yaw: -(face.yaw?.doubleValue ?? .nan) * degrees,
            pitch: (face.pitch?.doubleValue ?? .nan) * degrees,
            roll: (face.roll?.doubleValue ?? .nan) * degrees,
            centerX: bounds.midX, centerY: 1 - bounds.midY, width: bounds.width, height: bounds.height,
            leftEyeClosed: Self.closure(face.landmarks?.leftEye, bounds: bounds, width: image.width, height: image.height),
            rightEyeClosed: Self.closure(face.landmarks?.rightEye, bounds: bounds, width: image.width, height: image.height))
    }

    private static func closure(_ eye: VNFaceLandmarkRegion2D?, bounds: CGRect, width: Int, height: Int) -> Double {
        guard let eye, eye.pointCount >= 4 else { return .nan }
        let points = eye.normalizedPoints
        let xs = points.map { Double($0.x) * bounds.width * Double(width) }
        let ys = points.map { Double($0.y) * bounds.height * Double(height) }
        let span = (xs.max() ?? 0) - (xs.min() ?? 0)
        guard span > 0 else { return .nan }
        let aperture = ((ys.max() ?? 0) - (ys.min() ?? 0)) / span
        return min(1, max(0, (0.22 - aperture) / 0.14))
    }
}
#endif
