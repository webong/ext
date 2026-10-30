package io.github.webong.ext.ml;

import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;

public final class MlDigest {
    public static String sha256(byte[] data) throws MlException {
        try {
            MessageDigest md = MessageDigest.getInstance("SHA-256");
            byte[] digest = md.digest(data);
            StringBuilder sb = new StringBuilder(64);
            for (byte b : digest) sb.append(String.format("%02x", b));
            return sb.toString();
        } catch (NoSuchAlgorithmException e) {
            throw new MlException(MlException.Kind.NATIVE_FAILURE, "SHA-256 unavailable", e);
        }
    }
}
