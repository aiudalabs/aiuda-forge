package sandbox

import (
	"errors"
	"testing"
)

func TestMustDocker(t *testing.T) {
	cases := []struct {
		name    string
		require bool
		kind    string
		wantErr bool
	}{
		{"require+local fails", true, "local", true},
		{"require+docker ok", true, "docker", false},
		{"norequire+local ok", false, "local", false},
		{"norequire+docker ok", false, "docker", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{RequireDocker: c.require}
			// Build a real sandbox of the desired kind via New is daemon-dependent;
			// instead construct the concrete types directly.
			var sb Sandbox
			if c.kind == "docker" {
				sb = &DockerSandbox{cfg: cfg}
			} else {
				sb = &LocalSandbox{cfg: cfg}
			}
			err := cfg.MustDocker(sb)
			if c.wantErr && !errors.Is(err, ErrDockerRequired) {
				t.Errorf("want ErrDockerRequired, got %v", err)
			}
			if !c.wantErr && err != nil {
				t.Errorf("want nil, got %v", err)
			}
		})
	}
}
