# Wire's generated messages are found through their ADAPTER fields.
-keep class gochat.v1.** { *; }
-keepclassmembers class * extends com.squareup.wire.Message { public static final com.squareup.wire.ProtoAdapter ADAPTER; }
# Ktor and OkHttp name optional classes they do without.
-dontwarn org.slf4j.**
-dontwarn org.conscrypt.**
-dontwarn org.bouncycastle.**
-dontwarn org.openjsse.**
-dontwarn java.lang.management.**
