#if os(iOS) && canImport(UIKit)
import Foundation
import UIKit

/// Bridges iOS application lifecycle notifications to `CaptureJourney`.
///
/// Backgrounding only records guidance and keeps the persisted reference; the
/// foreground hook performs a read-only, idempotent re-attachment.
@MainActor
public final class IOSCaptureJourneyLifecycleObserver {
    private let journey: CaptureJourney
    private var observers: [NSObjectProtocol] = []

    public init(journey: CaptureJourney) {
        self.journey = journey
    }

    public func start() {
        guard observers.isEmpty else { return }
        let center = NotificationCenter.default
        observers.append(center.addObserver(
            forName: UIApplication.willResignActiveNotification,
            object: nil,
            queue: .main
        ) { [journey] _ in
            Task { _ = await journey.applicationEnteredBackground() }
        })
        observers.append(center.addObserver(
            forName: UIApplication.didBecomeActiveNotification,
            object: nil,
            queue: .main
        ) { [journey] _ in
            Task { _ = try? await journey.applicationEnteredForeground() }
        })
    }

    public func stop() {
        let center = NotificationCenter.default
        for observer in observers { center.removeObserver(observer) }
        observers.removeAll()
    }
}
#endif
