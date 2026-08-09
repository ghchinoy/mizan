package eval

// content.go holds helpers that turn an AssetRef into the Part representation
// each backend needs. Note the two backends use *different* Go types from
// *different* packages even though structurally similar:
//
//	cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb.Part  (native path)
//	google.golang.org/genai.Part                                 (custom path)
//
// so two small converters are needed rather than a shared type. These are
// implemented in the eval implementation phase (docs/spikes.md Spike 2).
