#if os(iOS)
import SwiftUI
import UIKit
import AVFoundation

/// Embeddable native journey. Core remains authoritative for consent, branches,
/// accepted evidence and completion. Requires NSCameraUsageDescription.
public struct CaptureView: View {
    @StateObject private var model: NativeCaptureModel
    @Environment(\.scenePhase) private var scenePhase
    @AccessibilityFocusState private var headingFocused: Bool

    public init(journey: CaptureJourney, bootstrapToken: String? = nil,
                acquisitionPlans: (any CaptureAcquisitionPlanProvider)? = nil,
                qualityAssessor: any CaptureQualityAssessor = AppleImageQualityAssessor()) {
        _model = StateObject(wrappedValue: NativeCaptureModel(journey: journey, bootstrapToken: bootstrapToken,
            acquisitionPlans: acquisitionPlans, qualityAssessor: qualityAssessor))
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                Text(verbatim: model.title).font(.largeTitle.bold())
                    .accessibilityAddTraits(.isHeader).accessibilityFocused($headingFocused)
                    .accessibilityIdentifier("capture.heading")
                if let message = model.message { Text(verbatim: message).accessibilityIdentifier("capture.message") }
                if model.busy { ProgressView("Please wait").frame(maxWidth: .infinity) }
                if !model.busy {
                    content
                }
                if model.snapshot.canCancel {
                    Button("Cancel Verification", role: .cancel) { model.cancel() }
                        .frame(minHeight: 44).accessibilityIdentifier("capture.cancel")
                }
            }
            .padding(24).frame(maxWidth: 560, alignment: .leading).frame(maxWidth: .infinity)
        }
        .background(model.screen == .review ? Color.black : Color(uiColor: .systemBackground))
        .foregroundStyle(model.screen == .review ? Color.white : Color.primary)
        .onChange(of: model.screen) { _ in headingFocused = true }
        .onChange(of: scenePhase) { phase in
            if phase == .background { model.lifecycle(active: false) }
            else if phase == .active { model.lifecycle(active: true) }
        }
        .onDisappear { model.suspend() }
        .sheet(isPresented: $model.cameraVisible, onDismiss: { model.cameraDismissed() }) {
            NativeCameraScreen(front: model.requirement?.camera == .front, title: model.captureTitle, side: model.sideLabel,
                completion: { result in model.receivedPhoto(result) }, requirement: model.requirement,
                qualityAssessor: model.qualityAssessor, sequenceCompletion: { model.receivedSequence($0) })
                .ignoresSafeArea().interactiveDismissDisabled()
        }
    }

    @ViewBuilder private var content: some View {
        switch model.screen {
        case .welcome:
            primary("Get Started") { model.load() }
        case .notice:
            if let state = model.noticeState {
                Text(verbatim: state.notice.copy.summary)
                noticeField("Purpose", state.notice.copy.purpose)
                noticeField("Controller", state.notice.controller)
                noticeField("Recipient", state.notice.recipient)
                noticeField("Consequences", state.notice.copy.consequences)
                noticeField("Jurisdiction", state.jurisdiction)
                noticeField("Processing Regions", state.regions.joined(separator: ", "))
                noticeField("Retention", state.retentionReference)
                primary(state.consentRequired ? "Agree & Continue" : "Acknowledge & Continue") { model.respond(refuse: false) }
                Button("I Do Not Agree", role: .destructive) { model.respond(refuse: true) }.frame(minHeight: 44)
            }
        case .choice:
            if let choice = model.choice {
                ForEach(choice.options) { option in
                    primary(option.label) { model.select(option.id) }
                }
            }
        case .prepare:
            Text(verbatim: model.isSelfie ? "Use even lighting and keep your face uncovered." : "Use your original \(model.documentName). Keep all four corners visible and avoid reflections.")
            primary("Start Camera") { model.openCamera() }
        case .review:
            Text(verbatim: model.sideLabel).font(.headline)
            if let data = model.photo?.bytes, let image = UIImage(data: data) {
                Image(uiImage: image).resizable().scaledToFit().frame(maxHeight: 360)
                    .padding(16).background(.black, in: RoundedRectangle(cornerRadius: 24))
                    .accessibilityLabel("Captured photo for review")
            }
            primary("Use Photo") { model.submit() }
            Button("Retake Photo") { model.retake() }.frame(minHeight: 44)
        case .processing:
            ProgressView("Waiting for Core")
            primary("Check Progress") { model.refresh() }
        case .recovery:
            primary("Try Again") { model.refresh() }
            Button("Open Settings") {
                if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
            }.frame(minHeight: 44)
        case .finished, .refused, .blocked:
            EmptyView()
        }
    }

    private func primary(_ title: String, action: @escaping () -> Void) -> some View {
        Button(action: action) { Text(verbatim: title).frame(maxWidth: .infinity, minHeight: 44) }
            .buttonStyle(.borderedProminent).accessibilityIdentifier("capture.primary")
    }

    private func noticeField(_ label: String, _ text: String) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(verbatim: label).font(.headline)
            Text(verbatim: text)
        }.accessibilityElement(children: .combine)
    }
}

@MainActor final class NativeCaptureModel: ObservableObject {
    enum Screen { case welcome, notice, choice, prepare, review, processing, recovery, finished, refused, blocked }
    @Published var screen: Screen = .welcome
    @Published var title = "Let’s Verify Your Identity"
    @Published var message: String?
    @Published var busy = false
    @Published var cameraVisible = false
    @Published var snapshot: CaptureJourneySnapshot = .idle
    @Published var noticeState: CaptureNoticeState?
    @Published var choice: CaptureDocumentChoice?
    @Published var photo: CapturedArtifact?
    let journey: CaptureJourney
    private var bootstrapToken: String?
    private var operation: Task<Void, Never>?
    private var generation = 0
    private var started = false
    private var backgrounded = false
    private let acquisitionPlans: (any CaptureAcquisitionPlanProvider)?
    let qualityAssessor: any CaptureQualityAssessor
    private(set) var requirement: CaptureRequirement?
    private(set) var documentName = "identity document"
    private(set) var captureTitle = "Take a Photo"
    var sideLabel: String { isSelfie ? "Selfie" : snapshot.currentTask?.artefact.hasSuffix(".document_back") == true ? "Back of document" : "Front of document" }
    var isSelfie: Bool { snapshot.currentTask?.evidenceType == "idenqa.evidence.selfie_image" }

    init(journey: CaptureJourney, bootstrapToken: String?, acquisitionPlans: (any CaptureAcquisitionPlanProvider)? = nil,
         qualityAssessor: any CaptureQualityAssessor = AppleImageQualityAssessor()) {
        self.journey = journey; self.bootstrapToken = bootstrapToken
        self.acquisitionPlans = acquisitionPlans; self.qualityAssessor = qualityAssessor
    }

    func load() {
        perform {
            if let token = self.bootstrapToken { self.snapshot = try await self.journey.start(bootstrapToken: token); self.bootstrapToken = nil }
            else { self.snapshot = try await self.journey.resume() }
            self.started = true
            try await self.route()
        }
    }

    func refresh() {
        if !started { load(); return }
        perform { self.snapshot = try await self.journey.refresh(); try await self.route() }
    }

    func respond(refuse: Bool) {
        guard let noticeState else { return }
        perform {
            self.noticeState = try await self.journey.respondToNotice(refuse ? .refuse : noticeState.consentRequired ? .consent : .acknowledge)
            try await self.route()
        }
    }

    func select(_ id: String) {
        guard let choice else { return }
        perform {
            self.snapshot = try await self.journey.selectDocument(requirementKey: choice.requirementKey, documentType: id)
            try await self.route()
        }
    }

    func openCamera() {
        perform {
            guard self.requirement != nil else { throw IdenqaError.invalidConfiguration }
            let authority = try await self.journey.notice()
            guard authority.accepted, !authority.refused else { throw IdenqaError.stateConflict }
            guard await AVCaptureDevice.requestAccess(for: .video) else { throw IdenqaError.cameraPermissionDenied }
            try Task.checkCancellation()
            _ = await self.journey.applySensitiveScreenProtection()
            self.cameraVisible = true
        }
    }

    func receivedPhoto(_ result: CapturedArtifact?) {
        cameraVisible = false
        guard let result else { return }
        perform {
            guard let requirement = self.requirement, requirement.challenges.isEmpty else { throw IdenqaError.invalidConfiguration }
            _ = try await AcquisitionCoordinator.validate(result, requirement: requirement, assessor: self.qualityAssessor)
            self.photo = result; self.screen = .review; self.title = "Check Your Photo"
            self.message = "Make sure the image is clear before continuing."
        }
    }

    func cameraDismissed() {} // Keep the sensitive-surface lifetime through review.
    func receivedSequence(_ frames: [AcquiredFrame]) {
        cameraVisible = false
        guard let task = snapshot.currentTask, let requirement else { return }
        perform {
            let authority = try await self.journey.notice()
            guard authority.accepted, !authority.refused else { throw IdenqaError.stateConflict }
            self.snapshot = try await self.journey.submitSequence(taskID: task.id, requirement: requirement, frames: frames)
            await self.journey.removeSensitiveScreenProtection()
            try await self.route()
        }
    }
    func retake() { photo = nil; screen = .prepare; openCamera() }

    func submit() {
        guard let task = snapshot.currentTask, let photo else { return }
        perform {
            let authority = try await self.journey.notice()
            guard authority.accepted, !authority.refused else { throw IdenqaError.stateConflict }
            guard let requirement = self.requirement, requirement.challenges.isEmpty else { throw IdenqaError.invalidConfiguration }
            _ = try await AcquisitionCoordinator.validate(photo, requirement: requirement, assessor: self.qualityAssessor)
            self.snapshot = try await self.journey.submit(taskID: task.id, artifact: photo, method: "idenqa.method.live_camera")
            self.photo = nil
            await self.journey.removeSensitiveScreenProtection()
            self.snapshot = try await self.journey.refresh()
            try await self.route()
        }
    }

    func cancel() {
        suspend()
        perform { _ = try await self.journey.cancel(); self.snapshot = await self.journey.currentSnapshot(); try await self.route() }
    }

    func suspend() {
        generation += 1; operation?.cancel(); operation = nil; busy = false
        cameraVisible = false; photo = nil
        Task { await journey.removeSensitiveScreenProtection() }
    }

    func lifecycle(active: Bool) {
        guard started else { return }
        if active { if backgrounded { backgrounded = false; refresh() } }
        else {
            backgrounded = true
            suspend(); screen = .recovery; title = "Your Progress Is Saved"
            message = "Return to continue. Any unfinished photo will need to be retaken."
            Task { _ = await journey.applicationEnteredBackground() }
        }
    }

    private func route() async throws {
        try Task.checkCancellation()
        message = nil; choice = nil
        switch snapshot.status {
        case .cancelled, .expired, .failed, .completed:
            screen = .finished
            title = snapshot.status == .completed ? "Verification Complete" : snapshot.status == .cancelled ? "Verification Cancelled" : snapshot.status == .expired ? "This Link Has Expired" : "Unable to Continue"
            message = snapshot.status == .completed ? "Return to the organisation that requested this check for the result." : nil
        case .processing, .awaitingExternal, .manualReview:
            screen = .processing; title = "Verifying Your Identity"
        case .captureRequired:
            let notice = try await journey.notice()
            try Task.checkCancellation()
            noticeState = notice
            if notice.refused { screen = .refused; title = "You Chose Not to Continue"; return }
            if !notice.accepted { screen = .notice; title = notice.notice.copy.title; return }
            if let pending = try await journey.documentChoice() { choice = pending; screen = .choice; title = "Choose Your Document"; return }
            guard let task = snapshot.currentTask else { screen = .processing; title = "Finishing Capture"; return }
            // An ordinary photograph must not silently stand in for an active challenge.
            guard task.methodOptions.contains("idenqa.method.live_camera") else {
                screen = .blocked; title = "Additional Capture Support Required"
                message = "This session requires an acquisition adapter that is not available in this native screen."; return
            }
            let label = await journey.documentLabel(for: task) ?? "identity document"
            guard let acquisitionPlans, let sessionID = snapshot.verificationID else {
                screen = .blocked; title = "Capture Setup Required"
                message = "The organisation must supply this session’s acquisition plan before capture can begin."; return
            }
            let plan = try await acquisitionPlans.plan(sessionID: sessionID)
            try Task.checkCancellation()
            requirement = try plan.requirement(sessionID: sessionID, task: task)
            guard let requirement, requirement.challenges.isEmpty ? task.requiredAssurances.isEmpty : requirement.challenges.count >= 2 && requirement.camera == .front else {
                screen = .blocked; title = "Additional Capture Support Required"
                message = "This acquisition plan requires a measured challenge adapter."; return
            }
            documentName = label
            screen = .prepare
            title = isSelfie ? "Take a Clear Selfie" : "Capture the \(task.artefact.hasSuffix(".document_back") ? "Back" : "Front") of Your \(label)"
            captureTitle = title
        default:
            screen = .blocked; title = "Unable to Continue"; message = "Contact the organisation that requested this check."
        }
    }

    private func perform(_ body: @escaping @MainActor () async throws -> Void) {
        guard !busy else { return }
        generation += 1; let current = generation; busy = true; message = nil
        operation = Task {
            defer { if self.generation == current { self.busy = false; self.operation = nil } }
            do { try await body() }
            catch {
                guard self.generation == current, !Task.isCancelled else { return }
                self.screen = .recovery; self.title = "Let’s Try That Again"
                self.message = error as? IdenqaError == .cameraPermissionDenied ? "Allow camera access in Settings to continue." : error as? IdenqaError == .captureQuality ? "The photo is not clear enough. Use even lighting, avoid glare, and retake it." : "We couldn’t finish this step. Check your connection and try again."
            }
        }
    }
}

#endif
