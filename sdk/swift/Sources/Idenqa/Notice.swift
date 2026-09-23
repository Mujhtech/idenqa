import Foundation

/// Exact Core-authored copy. Render as plain text, without rewriting or Markdown.
public struct CaptureNotice: Decodable, Equatable, Sendable {
    public let id: String
    public let locale: String
    public let controller: String
    public let recipient: String
    public let copy: Copy

    public struct Copy: Decodable, Equatable, Sendable {
        public let title: String
        public let summary: String
        public let purpose: String
        public let consequences: String
    }
}

public enum CaptureNoticeAction: String, Sendable { case acknowledge, consent, refuse }

public struct CaptureNoticeState: Sendable {
    public let notice: CaptureNotice
    public let consentRequired: Bool
    public let accepted: Bool
    public let refused: Bool
    public let jurisdiction: String
    public let retentionReference: String
    public let regions: [String]
}

struct NativeAuthoritySnapshot: Decodable, Sendable {
    struct Authority: Decodable, Sendable {
        let id: String
        let subject_id: String
        let verification_id: String
        let notice_id: String
        let consent_required: Bool
        let state: String
        let valid_from: Date
        let expires_at: Date
        let regions: [String]
        let jurisdiction: String
        let retention_reference: String
        let requirement_purposes: [String]
        let evidence_types: [String]
    }
    struct Response: Decodable, Sendable {
        let authority_id: String
        let notice_id: String
        let subject_id: String
        let verification_id: String
        let action: String
        let locale: String
    }
    let authority: Authority
    let notice: CaptureNotice
    let latest_response: Response?

    func validated(for session: CaptureSessionDocument, now: Date) throws -> CaptureNoticeState {
        let a = authority
        guard a.verification_id == session.id, a.notice_id == notice.id,
              a.state == "active", a.valid_from <= now, now < a.expires_at,
              a.regions.contains(session.region), !notice.locale.isEmpty,
              session.requirements.requirements.allSatisfy({
                  a.requirement_purposes.contains($0.purpose) && a.evidence_types.contains($0.evidenceType)
              }) else { throw IdenqaError.stateConflict }
        if let r = latest_response {
            guard r.authority_id == a.id, r.notice_id == notice.id,
                  r.subject_id == a.subject_id, r.verification_id == session.id,
                  r.locale == notice.locale, ["consent", "acknowledge", "refuse"].contains(r.action)
            else { throw IdenqaError.invalidResponse }
        }
        return CaptureNoticeState(notice: notice, consentRequired: a.consent_required,
            accepted: latest_response?.action == (a.consent_required ? "consent" : "acknowledge"),
            refused: latest_response?.action == "refuse", jurisdiction: a.jurisdiction,
            retentionReference: a.retention_reference, regions: a.regions)
    }
}

public struct CaptureDocumentOption: Codable, Equatable, Sendable, Identifiable {
    public let id: String
    public let label: String
    public let artefacts: [String]
}

public struct CaptureDocumentChoice: Sendable {
    public let requirementKey: String
    public let options: [CaptureDocumentOption]
}

extension IdenqaClient {
    func getAuthority() async throws -> NativeAuthoritySnapshot {
        let response = try await captureRequest(method: "GET", path: "v1/capture/authority")
        return try decode(NativeAuthoritySnapshot.self, from: response.body, status: response.status)
    }

    func respond(action: CaptureNoticeAction, locale: String, idempotencyKey: String) async throws {
        let body = try JSONSerialization.data(withJSONObject: ["action": action.rawValue, "locale": locale])
        let response = try await captureRequest(method: "POST", path: "v1/capture/authority/responses",
            headers: ["Content-Type": "application/json", "Idempotency-Key": try structuredString(idempotencyKey)], body: body)
        _ = try decode(NativeAuthoritySnapshot.Response.self, from: response.body, status: response.status)
    }

    func selectDocument(requirementKey: String, documentType: String, version: Int64, idempotencyKey: String) async throws -> CaptureSessionDocument {
        let body = try JSONSerialization.data(withJSONObject: ["requirement_key": requirementKey, "document_type": documentType, "expected_version": version])
        let response = try await captureRequest(method: "POST", path: "v1/capture/document-selection",
            headers: ["Content-Type": "application/json", "Idempotency-Key": try structuredString(idempotencyKey)], body: body)
        return try decode(CaptureSessionDocument.self, from: response.body, status: response.status)
    }
}
