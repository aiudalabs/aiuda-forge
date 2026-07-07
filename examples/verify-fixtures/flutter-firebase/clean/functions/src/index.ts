import * as functions from "firebase-functions";
import * as admin from "firebase-admin";
admin.initializeApp();
export const listBookings = functions.https.onCall(async () => {
  const db = admin.firestore();
  const snap = await db.collection("bookings").get();
  return snap.size;
});
