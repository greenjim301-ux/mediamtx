package conf

// ManagementChange is a durable change made by the administration UI.
// Existing configuration APIs retain their runtime-only behavior.
type ManagementChange struct {
	Action string          `json:"action"`
	Name   string          `json:"name,omitempty"`
	Path   *OptionalPath   `json:"path,omitempty"`
	Global *OptionalGlobal `json:"global,omitempty"`
}
