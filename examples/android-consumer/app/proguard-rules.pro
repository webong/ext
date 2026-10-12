# The app's own rules only: the test entry point is called from the test APK, which R8 cannot
# see. Nothing here keeps the SDK; the AAR must carry the rules its native code needs.
-keep class dev.ext.consumer.Roundtrips { *; }
-keep class dev.ext.consumer.WamrRoundtrip { *; }
