import Foundation

/// Sensitive-screen protection port.
///
/// The port never fails a capture journey when the platform cannot protect a
/// surface: `isAvailable` reports the honest state and the journey degrades to
/// guidance. `isCaptured` reports recording/mirroring state when the platform
/// exposes it.
public protocol CaptureScreenProtection: Sendable {
    var isAvailable: Bool { get }
    func applyProtection() async
    func removeProtection() async
    func isCaptured() async -> Bool
}

/// Graceful default used when a platform protection surface is unavailable.
public struct NoopCaptureScreenProtection: CaptureScreenProtection {
    public init() {}
    public var isAvailable: Bool { false }
    public func applyProtection() async {}
    public func removeProtection() async {}
    public func isCaptured() async -> Bool { false }
}

#if os(iOS) && canImport(UIKit)
import UIKit

/// iOS sensitive-screen protection.
///
/// `isCaptured` reflects `UIScreen.isCaptured` (screen recording, mirroring, or
/// AirPlay capture). Apply protection before presenting a capture preview and
/// remove it when the preview leaves the screen.
///
/// `CaptureSensitiveView` masks its contents while the screen is captured.
@MainActor
public final class IOSSensitiveScreenProtection: CaptureScreenProtection {
    public nonisolated let isAvailable = true
    private var protected = false

    public init() {}

    public func applyProtection() async {
        protected = true
    }

    public func removeProtection() async {
        protected = false
    }

    public func isCaptured() async -> Bool {
        UIScreen.main.isCaptured
    }

    public var isProtectionApplied: Bool { protected }
}

/// A view that masks its content while the screen is being captured or recorded.
@MainActor
public final class CaptureSensitiveView: UIView {
    private let maskLayer = UIView()

    public var maskColor: UIColor = .systemBackground {
        didSet { maskLayer.backgroundColor = maskColor }
    }

    public override init(frame: CGRect) {
        super.init(frame: frame)
        configure()
    }

    public required init?(coder: NSCoder) {
        super.init(coder: coder)
        configure()
    }

    private func configure() {
        maskLayer.backgroundColor = maskColor
        maskLayer.isHidden = true
        maskLayer.isUserInteractionEnabled = false
        maskLayer.translatesAutoresizingMaskIntoConstraints = false
        addSubview(maskLayer)
        NSLayoutConstraint.activate([
            maskLayer.leadingAnchor.constraint(equalTo: leadingAnchor),
            maskLayer.trailingAnchor.constraint(equalTo: trailingAnchor),
            maskLayer.topAnchor.constraint(equalTo: topAnchor),
            maskLayer.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
        NotificationCenter.default.addObserver(
            self,
            selector: #selector(captureStateChanged),
            name: UIScreen.capturedDidChangeNotification,
            object: nil
        )
        captureStateChanged()
    }

    @objc private func captureStateChanged() {
        maskLayer.isHidden = !UIScreen.main.isCaptured
    }
}
#endif
