import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

public struct TransportRequest: Sendable {
    public let method: String
    public let url: URL
    public let headers: [String: String]
    public let body: Data?
}

public struct TransportResponse: Sendable {
    public let status: Int
    public let body: Data
}

public protocol HTTPTransport: Sendable {
    func send(_ request: TransportRequest) async throws -> TransportResponse
}

public struct URLSessionTransport: HTTPTransport {
    private let session: URLSession

    public init(session: URLSession = .shared) { self.session = session }

    public func send(_ request: TransportRequest) async throws -> TransportResponse {
        var value = URLRequest(url: request.url)
        value.httpMethod = request.method
        value.httpBody = request.body
        value.timeoutInterval = 30
        for (name, header) in request.headers { value.setValue(header, forHTTPHeaderField: name) }
        let (body, response) = try await session.data(for: value)
        guard let http = response as? HTTPURLResponse else { throw IdenqaError.invalidResponse }
        return TransportResponse(status: http.statusCode, body: body)
    }
}

public protocol RealtimeTransport: Sendable {
    func events(url: URL, ticket: String) -> AsyncThrowingStream<RealtimeEvent, Error>
}

public struct URLSessionRealtimeTransport: RealtimeTransport {
    private let session: URLSession
    public init(session: URLSession = .shared) { self.session = session }

    public func events(url: URL, ticket: String) -> AsyncThrowingStream<RealtimeEvent, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                var request = URLRequest(url: url)
                request.setValue("Bearer \(ticket)", forHTTPHeaderField: "Authorization")
                let socket = session.webSocketTask(with: request)
                socket.resume()
                defer { socket.cancel(with: .goingAway, reason: nil) }
                do {
                    while !Task.isCancelled {
                        let message = try await socket.receive()
                        let data: Data
                        switch message {
                        case .data(let value): data = value
                        case .string(let value): data = Data(value.utf8)
                        @unknown default: throw IdenqaError.invalidResponse
                        }
                        continuation.yield(try JSONDecoder.idenqa.decode(RealtimeEvent.self, from: data))
                    }
                    continuation.finish()
                } catch is CancellationError {
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}

extension JSONDecoder {
    static var idenqa: JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }
}
