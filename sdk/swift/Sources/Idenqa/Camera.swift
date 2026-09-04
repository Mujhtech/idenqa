#if os(iOS) && canImport(AVFoundation)
@preconcurrency import AVFoundation
import Foundation

public actor AVFoundationPhotoCaptureSource: RawCaptureSource {
    private let position: AVCaptureDevice.Position

    public init(position: AVCaptureDevice.Position) { self.position = position }

    public func capture() async throws -> CapturedArtifact {
        guard await AVCaptureDevice.requestAccess(for: .video) else { throw IdenqaError.cameraPermissionDenied }
        let operation = try await MainActor.run { try CameraOperation(position: position) }
        return try await operation.capture()
    }
}

@MainActor
private final class CameraOperation: NSObject, AVCapturePhotoCaptureDelegate, @unchecked Sendable {
    private let session = AVCaptureSession()
    private let output = AVCapturePhotoOutput()
    private var continuation: CheckedContinuation<CapturedArtifact, Error>?

    init(position: AVCaptureDevice.Position) throws {
        guard let device = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: position) else { throw IdenqaError.cameraUnavailable }
        let input = try AVCaptureDeviceInput(device: device)
        guard session.canAddInput(input), session.canAddOutput(output) else { throw IdenqaError.cameraUnavailable }
        session.addInput(input)
        session.addOutput(output)
    }

    func capture() async throws -> CapturedArtifact {
        session.startRunning()
        defer { session.stopRunning() }
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                self.continuation = continuation
                output.capturePhoto(with: AVCapturePhotoSettings(format: [AVVideoCodecKey: AVVideoCodecType.jpeg]), delegate: self)
            }
        } onCancel: {
            Task { @MainActor in self.finish(.failure(CancellationError())) }
        }
    }

    nonisolated func photoOutput(_ output: AVCapturePhotoOutput, didFinishProcessingPhoto photo: AVCapturePhoto, error: Error?) {
        Task { @MainActor in
            if let error { finish(.failure(error)); return }
            guard let data = photo.fileDataRepresentation(), !data.isEmpty else { finish(.failure(IdenqaError.invalidResponse)); return }
            finish(.success(CapturedArtifact(bytes: data, contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera")))
        }
    }

    private func finish(_ result: Result<CapturedArtifact, Error>) {
        guard let continuation else { return }
        self.continuation = nil
        continuation.resume(with: result)
    }
}
#endif
