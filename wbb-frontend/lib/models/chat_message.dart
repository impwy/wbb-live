class ChatMessage {
  const ChatMessage({
    required this.id,
    required this.senderId,
    required this.senderName,
    required this.text,
    required this.sentAt,
  });
  final String id;
  final String senderId;
  final String senderName;
  final String text;
  final DateTime sentAt;

  factory ChatMessage.fromJson(Map<String, dynamic> json) {
    final sender = (json['sender'] as Map<String, dynamic>?) ?? const {};
    return ChatMessage(
      id: json['id'] as String,
      senderId: sender['id'] as String? ?? '',
      senderName: sender['displayName'] as String? ?? '',
      text: json['text'] as String? ?? '',
      sentAt: DateTime.parse(json['sentAt'] as String),
    );
  }
}
