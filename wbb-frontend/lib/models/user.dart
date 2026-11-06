/// Public account shape returned by the API (`{id, email, displayName}`).
class User {
  const User({required this.id, required this.email, required this.displayName});

  final String id;
  final String email;
  final String displayName;

  factory User.fromJson(Map<String, dynamic> json) => User(
        id: json['id'] as String,
        email: json['email'] as String,
        displayName: json['displayName'] as String,
      );

  Map<String, dynamic> toJson() => {'id': id, 'email': email, 'displayName': displayName};
}
