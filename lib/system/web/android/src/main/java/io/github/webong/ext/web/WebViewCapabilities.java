package io.github.webong.ext.web;

/** Native current-page and future-document script mechanics. */
public final class WebViewCapabilities {
    public final ScriptExecutionCapability currentPage, futureDocuments;
    public WebViewCapabilities(ScriptExecutionCapability currentPage, ScriptExecutionCapability futureDocuments) {
        this.currentPage = currentPage; this.futureDocuments = futureDocuments;
    }
}
