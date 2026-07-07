const functions = require("firebase-functions/v1");
const { initializeApp } = require("firebase-admin/app");
const { getFirestore } = require("firebase-admin/firestore");

// clean: the Admin SDK app is initialized once at module load (top-level, idempotent).
initializeApp();

// clean: a server-side trigger creates the users/{uid} profile the app depends on,
// so signup — not seeding — is what makes the profile exist (bug #7 exercised as behavior).
exports.onCreateUser = functions.auth.user().onCreate(async (user) => {
  await getFirestore().doc(`users/${user.uid}`).set({ email: user.email || null, createdAt: Date.now() });
});

// clean: the callable works on cold start because the Admin SDK is actually initialized.
exports.listBookings = functions.https.onCall(async () => {
  const snap = await getFirestore().collection("bookings").get();
  return { count: snap.size };
});
