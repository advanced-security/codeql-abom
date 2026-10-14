namespace Abom.WebAssembly.Services;

/// <summary>A safe validation reason returned by the audit endpoint, not a transport failure.</summary>
public sealed class AuditFilterException(string message) : HttpRequestException(message);
