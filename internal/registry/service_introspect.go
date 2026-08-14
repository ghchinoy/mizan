package registry

// Read-only introspection accessors for the Service's injected collaborators.
// They exist so the composition root's P2 wiring (wire.OpenService injecting the
// YAML codec + config-derived SyncConfig, design §3.1/§3.11) is directly
// assertable from the wire package, and so a caller can learn the pack file
// extension without importing the codec. They expose no mutation and no backend
// type beyond the registry package's own Codec/SyncConfig, so they do not widen
// the seam cmd/* depends on.

// Codec returns the pack codec the Service was constructed with (default
// YAMLCodec; overridden via WithCodec). wire.OpenService injects NewYAMLCodec().
func (s *Service) Codec() Codec { return s.codec }

// SyncConfig returns the ambient sync settings injected at construction.
// wire.OpenService derives these from config (PackCacheDir + DefaultTemplatesRepo).
func (s *Service) SyncConfig() SyncConfig { return s.syncCfg }
