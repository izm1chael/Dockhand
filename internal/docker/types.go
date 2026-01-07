package docker

// Container is a minimal container representation used by Dockhand to avoid
// direct dependency on the Docker SDK. Fields cover the data we need for
// reconciliation.
type Container struct {
	ID      string            `json:"Id"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Labels  map[string]string `json:"Labels"`
	Names   []string          `json:"Names"`
}
