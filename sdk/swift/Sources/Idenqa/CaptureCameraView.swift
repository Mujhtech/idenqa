#if os(iOS)
@preconcurrency import AVFoundation
import SwiftUI

/// All session mutation and delegate state belongs to `queue`. Only the preview
/// layer reads the session on the UI executor, as required by AVFoundation.
final class NativeCameraController: NSObject, AVCapturePhotoCaptureDelegate, RawCaptureSource, @unchecked Sendable {
    let session = AVCaptureSession()
    private let queue = DispatchQueue(label: "dev.idenqa.camera")
    private let output = AVCapturePhotoOutput()
    private var closed = false
    private var takingPhoto = false
    private var completion: (@MainActor @Sendable (CapturedArtifact?) -> Void)?
    private var deadline: DispatchWorkItem?

    func start(front: Bool, ready: @escaping @MainActor @Sendable (Bool) -> Void) {
        queue.async {
            guard !self.closed else { return }
            do {
                guard let device = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: front ? .front : .back) else { throw IdenqaError.cameraUnavailable }
                let input = try AVCaptureDeviceInput(device: device)
                self.session.beginConfiguration()
                self.session.sessionPreset = .photo
                guard self.session.canAddInput(input), self.session.canAddOutput(self.output) else {
                    self.session.commitConfiguration()
                    throw IdenqaError.cameraUnavailable
                }
                self.session.addInput(input)
                self.session.addOutput(self.output)
                self.session.commitConfiguration()
                self.session.startRunning()
                let running = self.session.isRunning
                Task { @MainActor in ready(running) }
            } catch { Task { @MainActor in ready(false) } }
        }
    }

    func capture(orientation: AVCaptureVideoOrientation, completion: @escaping @MainActor @Sendable (CapturedArtifact?) -> Void) {
        queue.async { [self] in
            guard !self.closed, !self.takingPhoto, self.session.isRunning else {
                Task { @MainActor in completion(nil) }; return
            }
            self.takingPhoto = true
            self.completion = completion
            if let connection = self.output.connection(with: .video), connection.isVideoOrientationSupported {
                connection.videoOrientation = orientation
                if connection.isVideoMirroringSupported { connection.isVideoMirrored = false }
            }
            let timeout = DispatchWorkItem { [weak self] in self?.finish(nil) }
            self.deadline = timeout
            self.queue.asyncAfter(deadline: .now() + 15, execute: timeout)
            self.output.capturePhoto(with: AVCapturePhotoSettings(format: [AVVideoCodecKey: AVVideoCodecType.jpeg]), delegate: self)
        }
    }

    func photoOutput(_ output: AVCapturePhotoOutput, didFinishProcessingPhoto photo: AVCapturePhoto, error: Error?) {
        let data = error == nil ? photo.fileDataRepresentation() : nil
        queue.async {
            self.finish(data.map { CapturedArtifact(bytes: $0, contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera") })
        }
    }

    private func finish(_ artifact: CapturedArtifact?) {
        guard !closed, takingPhoto else { return }
        takingPhoto = false
        deadline?.cancel(); deadline = nil
        let callback = completion; completion = nil
        Task { @MainActor in callback?(artifact) }
    }

    func stop() {
        queue.async {
            self.closed = true
            self.deadline?.cancel(); self.deadline = nil
            let callback = self.completion; self.completion = nil
            Task { @MainActor in callback?(nil) }
            self.session.stopRunning()
        }
    }

    func capture() async throws -> CapturedArtifact {
        try Task.checkCancellation()
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                capture(orientation: .portrait) { artifact in
                    if let artifact { continuation.resume(returning: artifact) }
                    else { continuation.resume(throwing: IdenqaError.cameraUnavailable) }
                }
            }
        } onCancel: { self.stop() }
    }
}

private final class NativePreviewView: UIView {
    override class var layerClass: AnyClass { AVCaptureVideoPreviewLayer.self }
    var preview: AVCaptureVideoPreviewLayer { layer as! AVCaptureVideoPreviewLayer }
    override func layoutSubviews() {
        super.layoutSubviews()
        guard let connection = preview.connection, connection.isVideoOrientationSupported else { return }
        connection.videoOrientation = cameraOrientation(window?.windowScene?.interfaceOrientation)
    }
}

private func cameraOrientation(_ orientation: UIInterfaceOrientation?) -> AVCaptureVideoOrientation {
    switch orientation {
    case .landscapeLeft: .landscapeLeft
    case .landscapeRight: .landscapeRight
    case .portraitUpsideDown: .portraitUpsideDown
    default: .portrait
    }
}

private struct NativeCameraPreview: UIViewRepresentable {
    let controller: NativeCameraController
    func makeUIView(context: Context) -> NativePreviewView {
        let view = NativePreviewView()
        view.preview.session = controller.session
        view.preview.videoGravity = .resizeAspectFill
        return view
    }
    func updateUIView(_ view: NativePreviewView, context: Context) {}
    static func dismantleUIView(_ view: NativePreviewView, coordinator: ()) { view.preview.session = nil }
}

struct NativeCameraScreen: View {
    let front: Bool
    let title: String
    let side: String
    let completion: (CapturedArtifact?) -> Void
    var requirement: CaptureRequirement? = nil
    var qualityAssessor: any CaptureQualityAssessor = AppleImageQualityAssessor()
    var sequenceCompletion: ([AcquiredFrame]) -> Void = { _ in }
    @State private var controller = NativeCameraController()
    @State private var ready = false
    @State private var capturing = false
    @State private var failed = false
    @State private var help = false
    @State private var active = true
    @State private var challengeIndex = 0
    @State private var measuredProgress = 0.0
    @State private var feedback = "Face forward"
    private var challenged: Bool { !(requirement?.challenges.isEmpty ?? true) }
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            VStack(spacing: 24) {
                HStack {
                    Button("Back") { active = false; controller.stop(); completion(nil) }
                    Spacer()
                    Button("Help") { help = true }
                }.frame(minHeight: 44)
                Text(verbatim: challenged ? feedback : title).font(.title2.bold()).multilineTextAlignment(.center)
                    .accessibilityAddTraits(.isHeader)
                Text(front ? "Keep your face centred and look at the camera." : "Fit the entire document inside the frame. Avoid glare and keep the text clear.")
                    .multilineTextAlignment(.center).padding(16)
                    .background(Color.white.opacity(0.12), in: RoundedRectangle(cornerRadius: 16))
                NativeCameraPreview(controller: controller)
                    .aspectRatio(front ? 0.8 : 1.58, contentMode: .fit)
                    .clipShape(RoundedRectangle(cornerRadius: front ? 100 : 24))
                    .overlay(RoundedRectangle(cornerRadius: front ? 100 : 24).stroke(.white, lineWidth: 3))
                    .overlay {
                        if challenged, let requirement {
                            ZStack {
                                ForEach(requirement.challenges.indices, id: \.self) { index in
                                    let count = Double(requirement.challenges.count)
                                    let fraction = index < challengeIndex ? 1.0 : index == challengeIndex ? measuredProgress : 0
                                    Circle().trim(from: Double(index)/count + 0.008, to: (Double(index)+1)/count - 0.008)
                                        .stroke(.white.opacity(0.3), lineWidth: 7)
                                    Circle().trim(from: Double(index)/count + 0.008, to: Double(index)/count + 0.008 + max(0,1/count-0.016)*fraction)
                                        .stroke(.green, style: StrokeStyle(lineWidth: 7,lineCap: .round))
                                }
                            }.rotationEffect(.degrees(-90)).padding(-10).accessibilityHidden(true)
                        }
                    }
                    .accessibilityLabel(front ? "Live selfie preview" : "Live document preview")
                Text(verbatim: side).font(.headline)
                if failed { Text("The camera could not finish. Go back and try again.").accessibilityAddTraits(.isStaticText) }
                if !challenged { Button {
                    capturing = true
                    let orientation = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first(where: { $0.activationState == .foregroundActive })?.interfaceOrientation
                    controller.capture(orientation: cameraOrientation(orientation)) { artifact in
                        guard active else { return }
                        if let artifact { active = false; controller.stop(); completion(artifact) }
                        else { capturing = false; failed = true }
                    }
                } label: {
                    Label(capturing ? "Taking Photo" : "Take Photo", systemImage: "camera.fill")
                        .frame(maxWidth: .infinity, minHeight: 48)
                }.buttonStyle(.borderedProminent).disabled(!ready || capturing || !active) }
                if challenged { Text("Follow each instruction. Your photo is captured automatically once the movement is complete.") }
                Spacer(minLength: 0)
            }.padding(24).frame(maxWidth: 560)
        }.foregroundStyle(.white)
        .alert("Capture Help", isPresented: $help) {
            Button("Got It", role: .cancel) {}
        } message: {
            Text(front ? "Remove anything covering your face. Use even lighting and keep the camera at eye level." : "Use the original document. Place it on a plain surface, hold the camera steady, and move away from reflections. Keep all four corners visible.")
        }
        .task { controller.start(front: front) { value in if active { ready = value; failed = !value } } }
        .task(id: ready) {
            guard ready, challenged, let requirement else { return }
            do {
                let coordinator = AcquisitionCoordinator(source: controller, assessor: qualityAssessor,
                    presenter: NativeChallengePresenter { challenge in
                        challengeIndex = requirement.challenges.firstIndex(where: { $0.id == challenge.id }) ?? 0
                        measuredProgress = 0; feedback = "Look straight at the camera"
                    }, tracker: VisionPoseTracker(), progress: { id, state in
                        await MainActor.run {
                            measuredProgress = state.fraction
                            let prompt = requirement.challenges.first(where: { $0.id == id })?.prompt ?? .neutral
                            feedback = nativePoseInstruction(prompt: prompt, feedback: state.feedback)
                        }
                    })
                let frames = try await coordinator.acquire(requirement)
                try Task.checkCancellation()
                guard active else { return }
                active = false; controller.stop(); sequenceCompletion(frames)
            } catch {
                guard active, !Task.isCancelled else { return }
                failed = true; feedback = "We couldn’t complete the movement. Go back and try again."
                controller.stop()
            }
        }
        .onDisappear { active = false; controller.stop() }
        .onChange(of: scenePhase) { phase in if phase == .background { active = false; controller.stop(); completion(nil) } }
    }
}

private struct NativeChallengePresenter: ChallengePresenter {
    let action: @MainActor @Sendable (LivenessChallenge) -> Void
    func present(_ challenge: LivenessChallenge) async throws { await action(challenge) }
}

func nativePoseInstruction(prompt: LivenessPrompt, feedback: String) -> String {
    switch feedback {
    case "find_face": return "Keep your face in view"
    case "one_face": return "Only one person should be in view"
    case "move_closer": return "Move a little closer"
    case "move_back": return "Move a little further away"
    case "center_face": return "Centre your face in the guide"
    case "face_forward": return "Look straight at the camera"
    case "hold_still": return "Hold still"
    case "open_eyes": return "Open your eyes"
    case "quality": return "Use even lighting and hold the camera steady"
    default:
        switch prompt {
        case .neutral: return "Look straight at the camera"
        case .turnLeft: return "Slowly turn your head left"
        case .turnRight: return "Slowly turn your head right"
        case .lookUp: return "Slowly look up"
        case .lookDown: return "Slowly look down"
        case .blink: return "Blink, then open your eyes"
        }
    }
}
#endif
