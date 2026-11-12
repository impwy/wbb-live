/// A stored replay of one finished broadcast session.
class Recording {
  const Recording({
    required this.id,
    required this.broadcastId,
    required this.broadcastTitle,
    required this.ownerName,
    required this.startedAt,
    required this.durationSeconds,
    required this.sizeBytes,
  });
  final String id;
  final String broadcastId;
  final String broadcastTitle;
  final String ownerName;
  final DateTime startedAt;
  final int durationSeconds;
  final int sizeBytes;

  factory Recording.fromJson(Map<String, dynamic> json) => Recording(
    id: json['id'] as String,
    broadcastId: json['broadcastId'] as String,
    broadcastTitle: json['broadcastTitle'] as String? ?? '',
    ownerName: json['ownerName'] as String? ?? '',
    startedAt: DateTime.parse(json['startedAt'] as String),
    durationSeconds: (json['durationSeconds'] as num).toInt(),
    sizeBytes: (json['sizeBytes'] as num).toInt(),
  );

  String get durationLabel {
    final h = durationSeconds ~/ 3600;
    final m = (durationSeconds % 3600) ~/ 60;
    final s = durationSeconds % 60;
    String two(int v) => v.toString().padLeft(2, '0');
    return h > 0 ? '$h:${two(m)}:${two(s)}' : '${two(m)}:${two(s)}';
  }
}
