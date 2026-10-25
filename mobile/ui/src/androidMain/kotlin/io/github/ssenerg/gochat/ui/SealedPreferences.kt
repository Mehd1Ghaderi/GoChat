package io.github.ssenerg.gochat.ui

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import io.github.ssenerg.gochat.core.state.KeyValueStore
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * The app's private preferences, each value sealed with a key the Android
 * Keystore holds and never hands out: the session token is not stored in
 * plain text, and a copy of the files opens nothing elsewhere.
 */
internal class SealedPreferences(context: Context) : KeyValueStore {
    private val prefs = context.getSharedPreferences("gochat", Context.MODE_PRIVATE)
    private val key: SecretKey? by lazy { runCatching { keystoreKey() }.getOrNull() }

    override fun get(key: String): String? {
        val raw = prefs.getString(key, null) ?: return null
        val sealed = raw.removePrefix(SEALED)
        if (sealed == raw) {
            // Stored before values were sealed: seal it now.
            put(key, raw)
            return raw
        }
        return runCatching { open(sealed) }.getOrNull()
    }

    override fun put(key: String, value: String?) {
        // Without the Keystore's key nothing is kept: the person signs in
        // again rather than leaving the token in plain text.
        val sealed = value?.let { runCatching { SEALED + seal(it) }.getOrNull() }
        prefs.edit().apply { if (sealed == null) remove(key) else putString(key, sealed) }.apply()
    }

    private fun seal(value: String): String {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, checkNotNull(key))
        return Base64.encodeToString(cipher.iv + cipher.doFinal(value.encodeToByteArray()), Base64.NO_WRAP)
    }

    private fun open(sealed: String): String {
        val bytes = Base64.decode(sealed, Base64.NO_WRAP)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, checkNotNull(key), GCMParameterSpec(TAG_BITS, bytes, 0, IV_BYTES))
        return cipher.doFinal(bytes, IV_BYTES, bytes.size - IV_BYTES).decodeToString()
    }

    private fun keystoreKey(): SecretKey {
        val store = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (store.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    private companion object {
        const val KEYSTORE = "AndroidKeyStore"
        const val ALIAS = "gochat.preferences"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val SEALED = "sealed:"
        const val IV_BYTES = 12
        const val TAG_BITS = 128
    }
}
