/// Public state of one broadcast session. It contains no grant or login data.
class LiveSession {
  const LiveSession({
    required this.id,
    required this.broadcastId,
    required this.state,
    required this.version,
    required this.startedAt,
    this.endedAt,
  });

  final String id;
  final String broadcastId;
  final String state;
  final int version;
  final DateTime startedAt;
  final DateTime? endedAt;

  factory LiveSession.fromJson(Map<String, dynamic> json) => LiveSession(
    id: json['id'] as String,
    broadcastId: json['broadcastId'] as String,
    state: json['state'] as String,
    version: (json['version'] as num).toInt(),
    startedAt: DateTime.parse(json['startedAt'] as String),
    endedAt: json['endedAt'] == null
        ? null
        : DateTime.parse(json['endedAt'] as String),
  );
}
