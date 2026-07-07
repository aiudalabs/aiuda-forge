import 'package:cloud_firestore/cloud_firestore.dart';
Stream<QuerySnapshot> myAppointments(String uid) {
  return FirebaseFirestore.instance
      .collection('bookings')
      .where('userId', isEqualTo: uid)
      .orderBy('date', descending: true)
      .snapshots();
}
