"""ext for Python: one package, one namespace per area.

Only ``ext_io.plugin`` (the guest SDK for ext.plugin/v1) exists; ``system``, ``web``
and ``int`` are reserved names, added when someone needs them. Import the area you use; importing ``ext_io`` loads none of them.
"""
