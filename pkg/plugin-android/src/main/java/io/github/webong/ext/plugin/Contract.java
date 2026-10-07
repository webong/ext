package io.github.webong.ext.plugin;

import java.util.List;

public record Contract(String name, String version, List<Operation> operations) {}
