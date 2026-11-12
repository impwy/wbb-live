/// Public broadcast object (docs/design.md §5).
class Broadcast {
  const Broadcast({
    required this.id,
    required this.ownerId,
    required this.ownerDisplayName,
    required this.title,
    required this.description,
    required this.version,
    required this.status,
    required this.createdAt,
    required this.updatedAt,
  });

  final String id;
  final String ownerId;
  final String ownerDisplayName;
  final String title;
  final String description;
  final int version;
  final String status; // ready | starting | live | stopping | ended
  final DateTime createdAt;
  final DateTime updatedAt;

  factory Broadcast.fromJson(Map<String, dynamic> json) {
    final owner = (json['owner'] as Map<String, dynamic>?) ?? const {};
    return Broadcast(
      id: json['id'] as String,
      ownerId: (json['ownerId'] as String?) ?? (owner['id'] as String? ?? ''),
      ownerDisplayName: owner['displayName'] as String? ?? '',
      title: json['title'] as String,
      description: (json['description'] as String?) ?? '',
      version: (json['version'] as num).toInt(),
      status: (json['status'] as String?) ?? 'ready',
      createdAt: DateTime.parse(json['createdAt'] as String),
      updatedAt: DateTime.parse(json['updatedAt'] as String),
    );
  }
}

/// One page of the broadcast list.
class BroadcastPage {
  const BroadcastPage({required this.items, required this.nextCursor});

  final List<Broadcast> items;
  final String nextCursor;
}
