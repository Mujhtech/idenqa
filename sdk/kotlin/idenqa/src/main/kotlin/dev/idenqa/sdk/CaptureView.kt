package dev.idenqa.sdk

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.BitmapFactory
import android.net.Uri
import android.provider.Settings
import android.view.View
import android.widget.*
import kotlinx.coroutines.*
import kotlin.coroutines.resume
import kotlin.coroutines.CoroutineContext

/** Native one-task-at-a-time UI. Forward permission results and host lifecycle. */
class CaptureView(
    private val activity: Activity,
    private val journey: CaptureJourney,
    bootstrapToken: String? = null,
    private val acquisitionPlans: CaptureAcquisitionPlanProvider? = null,
    private val qualityAssessor: CaptureQualityAssessor = AndroidImageQualityAssessor(),
) : ScrollView(activity) {
    private val mainDispatcher = object: CoroutineDispatcher() {
        override fun dispatch(context: CoroutineContext,block: Runnable) { activity.runOnUiThread(block) }
    }
    private val scope = CoroutineScope(SupervisorJob() + mainDispatcher)
    private var work: Job? = null
    private var token = bootstrapToken
    private var started = false
    private var snapshot = CaptureJourneySnapshot.Idle
    private var artifact: CapturedArtifact? = null
    private var camera: CaptureCameraView? = null
    private var busy = false
    private var permissionPending = false
    private var requirement: CaptureRequirement? = null
    private var captureTitle = "Take a Photo"
    private var sideLabel = ""
    private var darkSurface = false
    private var generation = 0
    private var frameWaiter: CancellableContinuation<CapturedArtifact>? = null
    private val column = LinearLayout(activity).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(24),dp(24),dp(24),dp(24))
    }

    init {
        setBackgroundColor(android.graphics.Color.WHITE)
        isFillViewport = true
        addView(column,LayoutParams(LayoutParams.MATCH_PARENT,LayoutParams.WRAP_CONTENT))
        heading("Let’s Verify Your Identity")
        button("Get Started") { load() }
    }

    private fun load() = perform {
        snapshot = token?.let { journey.start(it) } ?: journey.resume()
        token=null; started=true; route()
    }
    fun refresh() { if(!started) { load(); return }; perform { snapshot=journey.refresh(); route() } }

    private suspend fun route() {
        currentCoroutineContext().ensureActive()
        setSurface(false)
        column.removeAllViews()
        when(snapshot.status) {
            CaptureJourneyStatus.CAPTURE_REQUIRED -> {
                val notice=journey.notice()
                currentCoroutineContext().ensureActive()
                if(notice.refused) { heading("You Chose Not to Continue"); cancellation(); return }
                if(!notice.accepted) {
                    heading(notice.notice.title); text(notice.notice.summary)
                    field("Purpose",notice.notice.purpose); field("Controller",notice.notice.controller)
                    field("Recipient",notice.notice.recipient); field("Consequences",notice.notice.consequences)
                    field("Jurisdiction",notice.jurisdiction); field("Processing Regions",notice.regions.joinToString(", "))
                    field("Retention",notice.retentionReference)
                    button(if(notice.consentRequired) "Agree & Continue" else "Acknowledge & Continue") {
                        perform { journey.respondToNotice(if(notice.consentRequired) CaptureNoticeAction.CONSENT else CaptureNoticeAction.ACKNOWLEDGE); route() }
                    }
                    button("I Do Not Agree") { perform { journey.respondToNotice(CaptureNoticeAction.REFUSE); route() } }
                } else {
                    val choice=journey.documentChoice()
                    if(choice!=null) {
                        heading("Choose Your Document")
                        choice.options.forEach { option -> button(option.label) {
                            perform { snapshot=journey.selectDocument(choice.requirementKey,option.id); route() }
                        } }
                    } else {
                        val task=snapshot.currentTask
                        if(task==null) { heading("Finishing Capture"); button("Check Progress") { refresh() } }
                        else if("idenqa.method.live_camera" !in task.methodOptions) {
                            heading("Additional Capture Support Required")
                            text("This session requires an acquisition adapter that is not available in this native screen.")
                        } else {
                            val label=journey.documentLabel(task) ?: "identity document"
                            val sessionId = snapshot.verificationId ?: throw IdenqaException.NotStarted
                            if (acquisitionPlans == null) {
                                heading("Capture Setup Required"); text("The organisation must supply this session’s acquisition plan before capture can begin.")
                                cancellation(); return
                            }
                            requirement = acquisitionPlans.plan(sessionId).requirement(sessionId,task)
                            currentCoroutineContext().ensureActive()
                            if (requirement!!.challenges.isEmpty() && task.requiredAssurances.isNotEmpty() ||
                                requirement!!.challenges.isNotEmpty() && (requirement!!.challenges.size < 2 || requirement!!.camera != CaptureCamera.FRONT)) {
                                heading("Additional Capture Support Required"); text("This acquisition plan requires a measured challenge adapter.")
                                cancellation(); return
                            }
                            captureTitle = if(task.evidenceType=="idenqa.evidence.selfie_image") "Take a Clear Selfie" else "Capture the ${if(task.artefact.endsWith(".document_back")) "Back" else "Front"} of Your $label"
                            sideLabel = if(task.evidenceType=="idenqa.evidence.selfie_image") "Selfie" else if(task.artefact.endsWith(".document_back")) "Back of document" else "Front of document"
                            heading(captureTitle)
                            text(if(task.evidenceType=="idenqa.evidence.selfie_image") "Use even lighting and keep your face uncovered." else "Use your original $label. Keep all four corners visible and avoid reflections.")
                            button("Start Camera") { openCamera() }
                        }
                    }
                }
            }
            CaptureJourneyStatus.PROCESSING, CaptureJourneyStatus.AWAITING_EXTERNAL, CaptureJourneyStatus.MANUAL_REVIEW -> {
                heading("Verifying Your Identity"); column.addView(ProgressBar(activity)); button("Check Progress") { refresh() }
            }
            CaptureJourneyStatus.COMPLETED -> { heading("Verification Complete"); text("Return to the organisation that requested this check for the result.") }
            CaptureJourneyStatus.CANCELLED -> heading("Verification Cancelled")
            CaptureJourneyStatus.EXPIRED -> heading("This Link Has Expired")
            else -> { heading("Unable to Continue"); text("Contact the organisation that requested this check.") }
        }
        cancellation()
    }

    private fun openCamera() {
        if(activity.checkSelfPermission(Manifest.permission.CAMERA)!=PackageManager.PERMISSION_GRANTED) {
            permissionPending=true; activity.requestPermissions(arrayOf(Manifest.permission.CAMERA),PERMISSION_REQUEST); return
        }
        perform {
            val policy = requirement ?: throw IdenqaException.InvalidConfiguration
            val notice=journey.notice()
            if(!notice.accepted || notice.refused) throw IdenqaException.StateConflict
            currentCoroutineContext().ensureActive()
            journey.applySensitiveScreenProtection()
            column.removeAllViews(); setSurface(true)
            heading(captureTitle)
            text("Fit the entire required side inside the frame. Avoid glare and keep the camera steady.")
            val challenged=policy.challenges.isNotEmpty()
            val instruction=if(challenged) text("Look straight at the camera",22f) else null
            val ring=if(challenged) CapturePoseRing(activity,policy.challenges.size) else null
            val shutter=Button(activity).apply { text="Capture Photo"; isEnabled=false; minHeight=dp(48) }
            val preview=CaptureCameraView(activity,policy.camera == CaptureCamera.FRONT,
                ready={ if(challenged) startLiveness(policy,instruction!!,ring!!) else shutter.isEnabled=true },
                photo={ value ->
                    if(challenged) { val waiter=frameWaiter; frameWaiter=null; if(waiter?.isActive==true) waiter.resume(value) }
                    else receivedPhoto(value)
                }, failure={ frameWaiter?.cancel(); frameWaiter=null; work?.cancel(); busy=false; showRecovery("The camera is unavailable. Try again.") })
            camera=preview
            val frame = FrameLayout(activity).apply {
                background = android.graphics.drawable.GradientDrawable().apply {
                    setColor(android.graphics.Color.BLACK); cornerRadius=dp(24).toFloat(); setStroke(dp(3),android.graphics.Color.WHITE)
                }
                setPadding(dp(3),dp(3),dp(3),dp(3)); clipToOutline=true
                addView(preview,FrameLayout.LayoutParams(LayoutParams.MATCH_PARENT,LayoutParams.MATCH_PARENT))
                if(ring!=null) addView(ring,FrameLayout.LayoutParams(LayoutParams.MATCH_PARENT,LayoutParams.MATCH_PARENT))
            }
            column.addView(frame,LinearLayout.LayoutParams(LayoutParams.MATCH_PARENT,dp(280)))
            text(sideLabel)
            button("Help") {
                android.app.AlertDialog.Builder(activity).setTitle("Capture Help")
                    .setMessage("Use even lighting. Keep all four corners visible, hold the camera steady and move away from reflections. For a selfie, keep your face uncovered and the camera at eye level.")
                    .setPositiveButton("Got It",null).show()
            }
            shutter.setOnClickListener { shutter.isEnabled=false; preview.takePhoto() }
            if(!challenged) column.addView(shutter)
            button("Back") { work?.cancel(); busy=false; stopCamera(); refresh() }; cancellation()
        }
    }

    fun onRequestPermissionsResult(requestCode: Int,grantResults: IntArray) {
        if(requestCode!=PERMISSION_REQUEST || !permissionPending) return
        permissionPending=false
        if(grantResults.firstOrNull()==PackageManager.PERMISSION_GRANTED) openCamera()
        else showRecovery("Allow camera access in Settings to continue.")
    }

    private fun receivedPhoto(value: CapturedArtifact) {
        camera?.close(); camera=null
        perform {
            val policy = requirement ?: throw IdenqaException.InvalidConfiguration
            if (policy.challenges.isNotEmpty()) throw IdenqaException.InvalidConfiguration
            withContext(Dispatchers.Default) { validateCapturePhoto(value,policy,qualityAssessor) }
            currentCoroutineContext().ensureActive()
            showPhoto(value)
        }
    }

    private fun showPhoto(value: CapturedArtifact) {
        artifact=value; column.removeAllViews(); setSurface(true); heading("Check Your Photo"); text(sideLabel)
        val image=try { decodeUprightCapture(value,1280) } catch(_: Exception) { artifact=null; showRecovery("The photo could not be read. Try again."); return }
        column.addView(ImageView(activity).apply { setImageBitmap(image); adjustViewBounds=true; contentDescription="Captured photo for review" },
            LinearLayout.LayoutParams(LayoutParams.MATCH_PARENT,dp(360)))
        text("Make sure the image is clear before continuing.")
        button("Use Photo") { submit() }; button("Retake Photo") { artifact=null; openCamera() }; cancellation()
    }

    private fun submit() {
        val task=snapshot.currentTask ?: return; val photo=artifact ?: return
        perform {
            val notice=journey.notice()
            if(!notice.accepted || notice.refused) throw IdenqaException.StateConflict
            val policy = requirement ?: throw IdenqaException.InvalidConfiguration
            if (policy.challenges.isNotEmpty()) throw IdenqaException.InvalidConfiguration
            withContext(Dispatchers.Default) { validateCapturePhoto(photo,policy,qualityAssessor) }
            snapshot=journey.submit(task.id,photo,"idenqa.method.live_camera")
            artifact=null; journey.removeSensitiveScreenProtection(); snapshot=journey.refresh(); route()
        }
    }

    fun onHostStopped() {
        work?.cancel(); busy=false; permissionPending=false; artifact=null; stopCamera()
        if(started) {
            column.removeAllViews(); heading("Your Progress Is Saved"); text("Return to continue. Any unfinished photo will need to be retaken.")
            button("Continue") { refresh() }; cancellation()
            scope.launch { journey.applicationEnteredBackground() }
        }
    }
    fun onHostResumed() { if(started) refresh() }
    fun close() { onHostStopped(); token=null; scope.cancel() }
    private fun stopCamera() { frameWaiter?.cancel(); frameWaiter=null; camera?.close(); camera=null; scope.launch { journey.removeSensitiveScreenProtection() } }

    private fun startLiveness(policy: CaptureRequirement, instruction: TextView, ring: CapturePoseRing) {
        val task=snapshot.currentTask ?: return
        generation++; val current=generation; busy=true
        work=scope.launch {
            val tracker=MediaPipePoseTracker(activity)
            try {
                val source=RawCaptureSource {
                    withContext(mainDispatcher) {
                        suspendCancellableCoroutine { continuation ->
                            val preview=camera
                            if(preview==null) { continuation.cancel(); return@suspendCancellableCoroutine }
                            frameWaiter=continuation
                            continuation.invokeOnCancellation { activity.runOnUiThread { if(frameWaiter===continuation) frameWaiter=null } }
                            preview.takePhoto()
                        }
                    }
                }
                val coordinator=AcquisitionCoordinator(source,qualityAssessor,ChallengePresenter {
                    withContext(mainDispatcher) { instruction.text="Look straight at the camera" }
                },tracker,progress={ id, progress ->
                    withContext(mainDispatcher) {
                        val index=policy.challenges.indexOfFirst { it.id==id }
                        instruction.text=nativePoseInstruction(policy.challenges[index].prompt,progress.feedback)
                        ring.update(index,progress.fraction)
                    }
                })
                val frames=withContext(Dispatchers.Default) { coordinator.acquire(policy) }
                currentCoroutineContext().ensureActive()
                camera?.close(); camera=null
                instruction.text="Sending your capture"
                val authority=journey.notice()
                if(!authority.accepted || authority.refused) throw IdenqaException.StateConflict
                snapshot=journey.submitSequence(task.id,policy,frames)
                stopCamera(); route()
            } catch(error: CancellationException) { throw error }
            catch(_: Exception) { showRecovery("We couldn’t complete the movement. Check your lighting and connection, then try again.") }
            finally { withContext(NonCancellable+Dispatchers.Default) { tracker.close() }; if(generation==current) busy=false }
        }
    }

    private fun perform(operation: suspend () -> Unit) {
        if(busy) return
        generation++; val current=generation
        busy=true
        // Keep cancellation reachable while transport is in flight.
        column.removeAllViews(); heading("Please Wait"); column.addView(ProgressBar(activity)); cancellation()
        work=scope.launch {
            try { operation() }
            catch(error: CancellationException) { throw error }
            catch(error: Exception) { showRecovery(if(error == IdenqaException.CaptureQuality) "The photo is not clear enough. Use even lighting, avoid glare, and retake it." else "We couldn’t finish this step. Check your connection and try again.") }
            finally { if(generation==current) busy=false }
        }
    }
    private fun cancellation() {
        if(snapshot.canCancel) button("Cancel Verification") {
            work?.cancel(); busy=false; artifact=null; stopCamera()
            perform { journey.cancel(); snapshot=journey.currentSnapshot(); route() }
        }
    }
    private fun showRecovery(message: String) {
        stopCamera(); column.removeAllViews(); setSurface(false); heading("Let’s Try That Again"); text(message)
        button("Try Again") { refresh() }
        button("Open Settings") { activity.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS,Uri.parse("package:${activity.packageName}"))) }
        cancellation()
    }
    private fun heading(value: String) { text(value,28f).apply { if(android.os.Build.VERSION.SDK_INT>=28) isAccessibilityHeading=true; requestFocus() } }
    private fun field(label: String,value: String) { text(label,18f); text(value) }
    private fun text(value: String,size: Float=16f): TextView = TextView(activity).also {
        it.setTextColor(if(darkSurface) android.graphics.Color.WHITE else android.graphics.Color.BLACK)
        it.text=value; it.textSize=size; it.setPadding(0,dp(8),0,dp(12)); column.addView(it)
    }
    private fun button(value: String,action: () -> Unit) { column.addView(Button(activity).apply {
        text=value; minHeight=dp(48); isAllCaps=false; setOnClickListener { action() }
    },LinearLayout.LayoutParams(LayoutParams.MATCH_PARENT,LayoutParams.WRAP_CONTENT)) }
    private fun dp(value: Int): Int = (value*resources.displayMetrics.density).toInt()
    private fun setSurface(dark: Boolean) {
        darkSurface=dark
        setBackgroundColor(if(dark) android.graphics.Color.BLACK else android.graphics.Color.WHITE)
        column.setBackgroundColor(android.graphics.Color.TRANSPARENT)
    }
    companion object { const val PERMISSION_REQUEST = 0x1D01 }
}
