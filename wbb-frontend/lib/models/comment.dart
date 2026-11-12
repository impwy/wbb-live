/// A public broadcast comment.
class Comment {
  const Comment({
    required this.id,
    required this.authorId,
    required this.authorName,
    required this.body,
    required this.edited,
    required this.createdAt,
  });

  final String id;
  final String authorId;
  final String authorName;
  final String body;
  final bool edited;
  final DateTime createdAt;

  factory Comment.fromJson(Map<String, dynamic> json) {
    final author = (json['author'] as Map<String, dynamic>?) ?? const {};
    return Comment(
      id: json['id'] as String,
      authorId: author['id'] as String? ?? '',
      authorName: author['displayName'] as String? ?? '',
      body: json['body'] as String? ?? '',
      edited: json['edited'] == true,
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}
