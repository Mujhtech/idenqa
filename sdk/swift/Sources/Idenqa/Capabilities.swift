import Foundation

/// Honest platform capability advertisement.
///
/// Capability names guide method selection. They are never assurance evidence:
/// the advertisement never contains provenance, freshness, liveness, or
/// integrity claims, and a currently available method only states that the SDK
/// and device can attempt it.
public struct CaptureCapabilities: Equatable, Sendable {
    public let platform: String
    public let sdkVersion: String
    public let declaredMethods: [String]
    public let availableMethods: [String]
    public let cameraCapture: Bool
    public let fileUpload: Bool
    public let livenessChallenge: Bool

    public init(
        platform: String,
        sdkVersion: String,
        declaredMethods: [String],
        availableMethods: [String],
        cameraCapture: Bool,
        fileUpload: Bool,
        livenessChallenge: Bool
    ) throws {
        guard platform == "ios" || platform == "android" else { throw IdenqaError.invalidConfiguration }
        guard CaptureConfiguration.validOpaqueReference(sdkVersion, maximum: 64) else {
            throw IdenqaError.invalidConfiguration
        }
        guard declaredMethods.count <= 64, Set(declaredMethods).count == declaredMethods.count,
              declaredMethods.allSatisfy(CaptureConfiguration.validMethod) else {
            throw IdenqaError.invalidConfiguration
        }
        guard availableMethods.count <= declaredMethods.count,
              Set(availableMethods).count == availableMethods.count,
              availableMethods.allSatisfy(declaredMethods.contains) else {
            throw IdenqaError.invalidConfiguration
        }
        self.platform = platform
        self.sdkVersion = sdkVersion
        self.declaredMethods = declaredMethods
        self.availableMethods = availableMethods
        self.cameraCapture = cameraCapture
        self.fileUpload = fileUpload
        self.livenessChallenge = livenessChallenge
    }

    /// The only value sent to Core. `implemented_methods` is the declared set and
    /// `currently_available_methods` is the currently usable subset.
    public func advertisement() -> CapabilityAdvertisement {
        CapabilityAdvertisement(
            platform: platform,
            sdkVersion: sdkVersion,
            implementedMethods: declaredMethods,
            currentlyAvailableMethods: availableMethods
        )
    }

    /// Maps platform method names to this device's current availability.
    /// Unrecognised methods stay declared but unavailable, so selection fails closed.
    public func withCurrentAvailability() throws -> CaptureCapabilities {
        try CaptureCapabilities(
            platform: platform,
            sdkVersion: sdkVersion,
            declaredMethods: declaredMethods,
            availableMethods: declaredMethods.filter { method in
                switch method {
                case "idenqa.method.live_camera": return cameraCapture
                case "idenqa.method.file_upload": return fileUpload
                default: return false
                }
            },
            cameraCapture: cameraCapture,
            fileUpload: fileUpload,
            livenessChallenge: livenessChallenge
        )
    }
}

#if os(iOS) && canImport(AVFoundation)
@preconcurrency import AVFoundation

extension CaptureCapabilities {
    /// Current iOS capabilities. Camera availability is queried without prompting.
    public static func current(sdkVersion: String, declaredMethods: [String]) throws -> CaptureCapabilities {
        let camera = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .front) != nil
            || AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .back) != nil
        return try CaptureCapabilities(
            platform: "ios",
            sdkVersion: sdkVersion,
            declaredMethods: declaredMethods,
            availableMethods: [],
            cameraCapture: camera,
            fileUpload: true,
            livenessChallenge: true
        ).withCurrentAvailability()
    }
}
#endif
