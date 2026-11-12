class Donation {
  const Donation({
    required this.donorName,
    required this.amount,
    required this.message,
    required this.confirmedAt,
  });
  final String donorName;
  final int amount;
  final String message;
  final DateTime confirmedAt;

  factory Donation.fromJson(Map<String, dynamic> json) => Donation(
    donorName: json['donorName'] as String? ?? '',
    amount: (json['amount'] as num).toInt(),
    message: json['message'] as String? ?? '',
    confirmedAt: DateTime.parse(json['confirmedAt'] as String),
  );
}

class DonationSummary {
  const DonationSummary({
    required this.items,
    required this.total,
    required this.count,
  });
  final List<Donation> items;
  final int total;
  final int count;
}

class DonationOrder {
  const DonationOrder({
    required this.orderId,
    required this.amount,
    required this.orderName,
    required this.recipient,
    required this.customerKey,
    required this.mode,
    required this.clientKey,
  });
  final String orderId;
  final int amount;
  final String orderName;
  final String recipient;
  final String customerKey;

  /// "toss" (Toss Payments test keys) or "test" (in-server test gateway).
  final String mode;
  final String clientKey;
}
