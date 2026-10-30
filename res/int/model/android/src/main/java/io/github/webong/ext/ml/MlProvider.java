package io.github.webong.ext.ml;

public interface MlProvider {
    String name();
    java.util.List<String> capabilities();
    MlSession load(MlInstalledModel model) throws MlException;
}
