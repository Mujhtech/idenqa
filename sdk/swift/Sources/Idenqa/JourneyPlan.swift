import Foundation

/// Derives the ordered one-task-at-a-time capture plan from the immutable session
/// snapshot, the advertised capabilities, and authoritative accepted progress.
///
/// Unknown namespaced methods fail closed: a method that is not declared by this
/// SDK and currently available on the device is never selected, and no local
/// fallback is invented. Only policy-approved fallbacks are used.
enum CapturePlanBuilder {
    static func build(
        session: CaptureSessionDocument,
        capabilities: CaptureCapabilities,
        preferences: [String],
        progress: [CaptureCompletionDocument],
        captureFailedTaskIDs: Set<String>
    ) throws -> CapturePlan {
        guard !session.requirements.requirements.isEmpty else { throw IdenqaError.invalidResponse }
        var tasks: [CaptureTask] = []
        for var requirement in session.requirements.requirements {
            if let selected = session.documentSelections?[requirement.key] {
                guard let option = requirement.documentOptions?.first(where: { $0.id == selected }) else { throw IdenqaError.invalidResponse }
                requirement.artefacts = option.artefacts
            }
            // Core canonicalises names lexically; the subject captures front first.
            requirement.artefacts.sort { left, right in
                func order(_ value: String) -> Int { value.hasSuffix(".document_front") ? 0 : value.hasSuffix(".document_back") ? 1 : 2 }
                return order(left) < order(right)
            }
            guard !requirement.key.isEmpty, !requirement.evidenceType.isEmpty, !requirement.artefacts.isEmpty,
                  !requirement.artefacts.contains(where: \.isEmpty) else {
                throw IdenqaError.invalidResponse
            }
            let selected = try selectAcquisition(
                acquisition: requirement.acquisition,
                fallbacks: requirement.fallbacks,
                capabilities: capabilities,
                preferences: preferences
            )
            var requirementTasks = makeTasks(
                requirement: requirement,
                methods: selected.methods,
                fallbackCondition: selected.fallback
            )
            if selected.fallback == nil {
                requirementTasks = try applyingCaptureFailure(
                    requirement: requirement,
                    tasks: requirementTasks,
                    capabilities: capabilities,
                    preferences: preferences,
                    failedTaskIDs: captureFailedTaskIDs
                )
            }
            tasks.append(contentsOf: requirementTasks)
        }
        let completed = try completionIDs(tasks: tasks, progress: progress)
        return CapturePlan(
            verificationID: session.id,
            profileRevision: session.profileRevision,
            profileDigest: session.profileDigest,
            tasks: tasks,
            completedTaskIDs: completed
        )
    }

    struct SelectedAcquisition {
        let methods: [String]
        let fallback: CaptureFallbackCondition?
    }

    static func selectAcquisition(
        acquisition: CaptureAcquisition,
        fallbacks: [CaptureFallback],
        capabilities: CaptureCapabilities,
        preferences: [String]
    ) throws -> SelectedAcquisition {
        let primary = evaluate(acquisition, capabilities: capabilities, preferences: preferences)
        if let methods = primary.methods { return SelectedAcquisition(methods: methods, fallback: nil) }
        for fallback in fallbacks {
            guard let condition = fallbackCondition(fallback, unavailable: primary.unavailable) else { continue }
            guard condition != .captureFailed else { continue }
            let candidate = evaluate(fallback.acquisition, capabilities: capabilities, preferences: preferences)
            if let methods = candidate.methods {
                return SelectedAcquisition(methods: methods, fallback: condition)
            }
        }
        throw IdenqaError.noCompatibleMethod
    }

    private struct Evaluation {
        let methods: [String]?
        let unavailable: [CaptureFallbackCondition]
    }

    private static func evaluate(
        _ acquisition: CaptureAcquisition,
        capabilities: CaptureCapabilities,
        preferences: [String]
    ) -> Evaluation {
        guard acquisition.strategy == "any_of" || acquisition.strategy == "all_of" else {
            return Evaluation(methods: nil, unavailable: [])
        }
        let methods = acquisition.methods
        guard !methods.isEmpty, Set(methods).count == methods.count else {
            return Evaluation(methods: nil, unavailable: [])
        }
        let supported = methods.filter(capabilities.declaredMethods.contains)
        let available = methods.filter(capabilities.availableMethods.contains)
        let ordered = orderedMethods(available, preferences: preferences)
        if acquisition.strategy == "any_of", !ordered.isEmpty {
            return Evaluation(methods: ordered, unavailable: [])
        }
        if acquisition.strategy == "all_of", ordered.count == methods.count {
            return Evaluation(methods: ordered, unavailable: [])
        }
        var unavailable: [CaptureFallbackCondition] = []
        if supported.count != methods.count { unavailable.append(.methodUnavailable) }
        if supported.contains(where: { !capabilities.availableMethods.contains($0) }) {
            unavailable.append(.capabilityUnavailable)
        }
        return Evaluation(methods: nil, unavailable: unavailable)
    }

    private static func orderedMethods(_ methods: [String], preferences: [String]) -> [String] {
        guard !preferences.isEmpty else { return methods }
        let preferred = preferences.filter(methods.contains)
        guard !preferred.isEmpty else { return methods }
        return preferred + methods.filter { !preferred.contains($0) }
    }

    private static func fallbackCondition(
        _ fallback: CaptureFallback,
        unavailable: [CaptureFallbackCondition]
    ) -> CaptureFallbackCondition? {
        for value in fallback.on {
            guard let condition = CaptureFallbackCondition(rawValue: value), condition != .captureFailed else { continue }
            if unavailable.contains(condition) { return condition }
        }
        return nil
    }

    private static func makeTasks(
        requirement: CaptureRequirementDocument,
        methods: [String],
        fallbackCondition: CaptureFallbackCondition?,
        artefacts: [String]? = nil
    ) -> [CaptureTask] {
        let values = artefacts ?? requirement.artefacts
        if methods.count == 1 {
            return values.map { task(requirement, artefact: $0, methods: methods, fallback: fallbackCondition) }
        }
        let strategy = requirement.acquisition.strategy
        if strategy == "any_of" {
            return values.map { task(requirement, artefact: $0, methods: methods, fallback: fallbackCondition) }
        }
        return values.flatMap { artefact in
            methods.map { method in task(requirement, artefact: artefact, methods: [method], fallback: fallbackCondition) }
        }
    }

    private static func applyingCaptureFailure(
        requirement: CaptureRequirementDocument,
        tasks: [CaptureTask],
        capabilities: CaptureCapabilities,
        preferences: [String],
        failedTaskIDs: Set<String>
    ) throws -> [CaptureTask] {
        guard !failedTaskIDs.isEmpty, tasks.contains(where: { failedTaskIDs.contains($0.id) }) else { return tasks }
        let failedArtefacts = Set(tasks.filter { failedTaskIDs.contains($0.id) }.map(\.artefact))
        var replacement: [String: [CaptureTask]] = [:]
        for fallback in requirement.fallbacks where fallback.on.contains(CaptureFallbackCondition.captureFailed.rawValue) {
            let evaluation = evaluate(fallback.acquisition, capabilities: capabilities, preferences: preferences)
            guard let methods = evaluation.methods else { continue }
            for artefact in failedArtefacts where replacement[artefact] == nil {
                replacement[artefact] = makeTasks(
                    requirement: requirement,
                    methods: methods,
                    fallbackCondition: .captureFailed,
                    artefacts: [artefact]
                )
            }
        }
        guard failedArtefacts.allSatisfy({ replacement[$0] != nil }) else {
            throw IdenqaError.noCompatibleMethod
        }
        return tasks.flatMap { task in replacement[task.artefact] ?? [task] }
    }

    private static func task(
        _ requirement: CaptureRequirementDocument,
        artefact: String,
        methods: [String],
        fallback: CaptureFallbackCondition?
    ) -> CaptureTask {
        let id = taskID(
            requirementKey: requirement.key,
            artefact: artefact,
            methods: methods,
            fallback: fallback
        )
        return CaptureTask(
            id: id,
            requirementKey: requirement.key,
            evidenceType: requirement.evidenceType,
            artefact: artefact,
            methodOptions: methods,
            fallbackCondition: fallback,
            requiredAssurances: requirement.requiredAssurances
        )
    }

    static func taskID(
        requirementKey: String,
        artefact: String,
        methods: [String],
        fallback: CaptureFallbackCondition?
    ) -> String {
        [requirementKey, artefact, methods.joined(separator: "+"), fallback?.rawValue ?? ""]
            .joined(separator: "::")
    }

    private static func completionIDs(
        tasks: [CaptureTask],
        progress: [CaptureCompletionDocument]
    ) throws -> Set<String> {
        var completed: Set<String> = []
        for completion in progress {
            let matches = tasks.filter { task in
                task.requirementKey == completion.requirementKey &&
                    task.evidenceType == completion.evidenceType &&
                    task.artefact == completion.artefact &&
                    task.methodOptions.contains(completion.acquisitionMethod) &&
                    (task.fallbackCondition?.rawValue ?? "") == (completion.fallbackCondition ?? "")
            }
            guard matches.count == 1 else { throw IdenqaError.invalidResponse }
            guard completed.insert(matches[0].id).inserted else { throw IdenqaError.invalidResponse }
        }
        return completed
    }
}
