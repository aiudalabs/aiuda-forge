const functions = require("firebase-functions/v1");
const { getFirestore } = require("firebase-admin/firestore");

// bug #5: the Admin SDK app is NEVER initialized -> the callable throws on cold start.
// bug #7: there is NO onCreate trigger -> users/{uid} is never created at signup.

exports.listBookings = functions.https.onCall(async () => {
  const snap = await getFirestore().collection("bookings").get(); // throws: no default app
  return { count: snap.size };
});
