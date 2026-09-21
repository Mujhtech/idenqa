package dev.idenqa.sdk

/**
 * Derives the ordered one-task-at-a-time capture plan from the immutable session
 * snapshot, the advertised capabilities, and authoritative accepted progress.
 *
 * Unknown namespaced methods fail closed: a method that is not declared by this
 * SDK and currently available on the device is never selected, and no local
 * fallback is invented. Only policy-approved fallbacks are used.
 */
internal object CapturePlanBuilder {
    data class SelectedAcquisition(val methods: List<String>, val fallback: CaptureFallbackCondition?)

    fun build(
        session: CaptureSessionDetail,
        capabilities: CaptureCapabilities,
        preferences: List<String>,
        progress: List<CaptureCompletion>,
        captureFailedTaskIds: Set<String>,
    ): CapturePlan {
        if (session.requirements.requirements.isEmpty()) throw IdenqaException.InvalidResponse
        val tasks = mutableListOf<CaptureTask>()
        for (requirement in session.requirements.requirements) {
            if (requirement.key.isEmpty() || requirement.evidenceType.isEmpty() || requirement.artefacts.isEmpty() ||
                requirement.artefacts.any { it.isEmpty() }
            ) throw IdenqaException.InvalidResponse
            val selected = selectAcquisition(
                requirement.acquisition, requirement.fallbacks, capabilities, preferences,
            )
            var requirementTasks = makeTasks(requirement, selected.methods, selected.fallback, null)
            if (selected.fallback == null) {
                requirementTasks = applyCaptureFailure(
                    requirement, requirementTasks, capabilities, preferences, captureFailedTaskIds,
                )
            }
            tasks += requirementTasks
        }
        return CapturePlan(
            verificationId = session.id,
            profileRevision = session.profileRevision,
            profileDigest = session.profileDigest,
            tasks = tasks,
            completedTaskIds = completionIds(tasks, progress),
        )
    }

    fun selectAcquisition(
        acquisition: CaptureAcquisition,
        fallbacks: List<CaptureFallback>,
        capabilities: CaptureCapabilities,
        preferences: List<String>,
    ): SelectedAcquisition {
        if (acquisition.strategy != "any_of" && acquisition.strategy != "all_of") throw IdenqaException.InvalidResponse
        val primary = evaluate(acquisition, capabilities, preferences)
        primary.methods?.let { return SelectedAcquisition(it, null) }
        for (fallback in fallbacks) {
            val condition = fallbackCondition(fallback, primary.unavailable) ?: continue
            if (condition == CaptureFallbackCondition.CAPTURE_FAILED) continue
            val candidate = evaluate(fallback.acquisition, capabilities, preferences)
            candidate.methods?.let { return SelectedAcquisition(it, condition) }
        }
        throw IdenqaException.NoCompatibleMethod
    }

    private data class Evaluation(val methods: List<String>?, val unavailable: List<CaptureFallbackCondition>)

    private fun evaluate(
        acquisition: CaptureAcquisition,
        capabilities: CaptureCapabilities,
        preferences: List<String>,
    ): Evaluation {
        val methods = acquisition.methods
        if (methods.isEmpty() || methods.toSet().size != methods.size) return Evaluation(null, emptyList())
        val supported = methods.filter { it in capabilities.declaredMethods }
        val available = methods.filter { it in capabilities.availableMethods }
        val ordered = orderedMethods(available, preferences)
        if (acquisition.strategy == "any_of" && ordered.isNotEmpty()) return Evaluation(ordered, emptyList())
        if (acquisition.strategy == "all_of" && ordered.size == methods.size) return Evaluation(ordered, emptyList())
        val unavailable = mutableListOf<CaptureFallbackCondition>()
        if (supported.size != methods.size) unavailable += CaptureFallbackCondition.METHOD_UNAVAILABLE
        if (supported.any { it !in capabilities.availableMethods }) unavailable += CaptureFallbackCondition.CAPABILITY_UNAVAILABLE
        return Evaluation(null, unavailable)
    }

    private fun orderedMethods(methods: List<String>, preferences: List<String>): List<String> {
        if (preferences.isEmpty()) return methods
        val preferred = preferences.filter { it in methods }
        if (preferred.isEmpty()) return methods
        return preferred + methods.filter { it !in preferred }
    }

    private fun fallbackCondition(
        fallback: CaptureFallback,
        unavailable: List<CaptureFallbackCondition>,
    ): CaptureFallbackCondition? = fallback.on
        .mapNotNull(CaptureFallbackCondition::fromWire)
        .firstOrNull { it != CaptureFallbackCondition.CAPTURE_FAILED && it in unavailable }

    private fun makeTasks(
        requirement: CaptureRequirementDocument,
        methods: List<String>,
        fallbackCondition: CaptureFallbackCondition?,
        artefacts: List<String>?,
    ): List<CaptureTask> {
        val values = artefacts ?: requirement.artefacts
        if (methods.size == 1 || requirement.acquisition.strategy == "any_of") {
            return values.map { task(requirement, it, methods, fallbackCondition) }
        }
        return values.flatMap { artefact ->
            methods.map { method -> task(requirement, artefact, listOf(method), fallbackCondition) }
        }
    }

    private fun applyCaptureFailure(
        requirement: CaptureRequirementDocument,
        tasks: List<CaptureTask>,
        capabilities: CaptureCapabilities,
        preferences: List<String>,
        failedTaskIds: Set<String>,
    ): List<CaptureTask> {
        if (failedTaskIds.isEmpty() || tasks.none { it.id in failedTaskIds }) return tasks
        val failedArtefacts = tasks.filter { it.id in failedTaskIds }.map { it.artefact }.toSet()
        val replacement = mutableMapOf<String, List<CaptureTask>>()
        for (fallback in requirement.fallbacks) {
            if (CaptureFallbackCondition.CAPTURE_FAILED.wire !in fallback.on) continue
            val methods = evaluate(fallback.acquisition, capabilities, preferences).methods ?: continue
            for (artefact in failedArtefacts) {
                replacement.getOrPut(artefact) { makeTasks(requirement, methods, CaptureFallbackCondition.CAPTURE_FAILED, listOf(artefact)) }
            }
        }
        if (failedArtefacts.any { it !in replacement }) throw IdenqaException.NoCompatibleMethod
        return tasks.flatMap { replacement[it.artefact] ?: listOf(it) }
    }

    private fun task(
        requirement: CaptureRequirementDocument,
        artefact: String,
        methods: List<String>,
        fallback: CaptureFallbackCondition?,
    ): CaptureTask = CaptureTask(
        id = taskId(requirement.key, artefact, methods, fallback),
        requirementKey = requirement.key,
        evidenceType = requirement.evidenceType,
        artefact = artefact,
        methodOptions = methods,
        fallbackCondition = fallback,
        requiredAssurances = requirement.requiredAssurances,
    )

    fun taskId(
        requirementKey: String,
        artefact: String,
        methods: List<String>,
        fallback: CaptureFallbackCondition?,
    ): String = listOf(requirementKey, artefact, methods.joinToString("+"), fallback?.wire ?: "").joinToString("::")

    private fun completionIds(tasks: List<CaptureTask>, progress: List<CaptureCompletion>): Set<String> {
        val completed = mutableSetOf<String>()
        for (completion in progress) {
            val matches = tasks.filter { task ->
                task.requirementKey == completion.requirementKey &&
                    task.evidenceType == completion.evidenceType &&
                    task.artefact == completion.artefact &&
                    completion.acquisitionMethod in task.methodOptions &&
                    (task.fallbackCondition?.wire ?: "") == (completion.fallbackCondition ?: "")
            }
            if (matches.size != 1) throw IdenqaException.InvalidResponse
            if (!completed.add(matches[0].id)) throw IdenqaException.InvalidResponse
        }
        return completed
    }
}
