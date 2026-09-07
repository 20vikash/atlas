package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	o, err := load(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if o != defaultOpts() {
		t.Fatalf("got %+v, want defaults %+v", o, defaultOpts())
	}
}

func TestLoadFileOverridesDefault(t *testing.T) {
	path := writeConfig(t, "[metald]\nlisten = \"0.0.0.0:9000\"\n[zfs]\npool = \"tank\"\n")
	o, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if o.listen != "0.0.0.0:9000" {
		t.Errorf("listen = %q, want the file value", o.listen)
	}
	if o.pool != "tank" {
		t.Errorf("pool = %q, want the file value", o.pool)
	}
	if o.imagesDir != defaultOpts().imagesDir {
		t.Errorf("imagesDir = %q, want the default for an unset key", o.imagesDir)
	}
}

func TestMeshEnabledByDefaultAndDisabledByConfig(t *testing.T) {
	if !defaultOpts().mesh.enabled {
		t.Fatal("mesh must be enabled by default")
	}

	// An absent enabled key keeps the default; only an explicit false disables it.
	present, err := load(writeConfig(t, "[wg_mesh]\nuplink = \"eth0\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !present.mesh.enabled {
		t.Error("mesh became disabled without an explicit enabled=false")
	}

	off, err := load(writeConfig(t, "[wg_mesh]\nenabled = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if off.mesh.enabled {
		t.Error("enabled=false did not disable the mesh")
	}
}

func TestLoadBaseDirMovesDerivedDirs(t *testing.T) {
	path := writeConfig(t, "[metald]\nbase_dir = \"/srv/metal\"\n")
	o, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if o.cfg.MachinesDir != "/srv/metal/machines" {
		t.Errorf("machinesDir = %q", o.cfg.MachinesDir)
	}
	if o.imagesDir != "/srv/metal/images" {
		t.Errorf("imagesDir = %q", o.imagesDir)
	}
}

func TestLoadAuthenticationTokenHash(t *testing.T) {
	const tokenHash = "4c5dc9b7708905f77f5e5d16316b5dfb425e68cb326dcd55a860e90a7707031e"
	path := writeConfig(t, "[metald]\nauth_token_hash = \""+tokenHash+"\"\n")
	options, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if options.authTokenHash != tokenHash {
		t.Errorf("authTokenHash = %q, want configured hash", options.authTokenHash)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := load("/no/such/config.toml"); err == nil {
		t.Error("explicit missing path: want error, got nil")
	}
}

func TestLoadSleepConfiguration(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantErr     bool
		wantEnabled bool
		wantTimeout time.Duration
	}{
		{name: "absent section is disabled", body: "", wantEnabled: false, wantTimeout: 0},
		{name: "enabled with a positive timeout", body: "[sleep]\nenabled = true\nidle_timeout = \"30m\"\n", wantEnabled: true, wantTimeout: 30 * time.Minute},
		{name: "disabled keeps a parsed timeout", body: "[sleep]\nenabled = false\nidle_timeout = \"30m\"\n", wantEnabled: false, wantTimeout: 30 * time.Minute},
		{name: "disabled without a timeout", body: "[sleep]\nenabled = false\n", wantEnabled: false, wantTimeout: 0},
		{name: "enabled without a timeout is rejected", body: "[sleep]\nenabled = true\n", wantErr: true},
		{name: "enabled with a zero timeout is rejected", body: "[sleep]\nenabled = true\nidle_timeout = \"0s\"\n", wantErr: true},
		{name: "enabled with a negative timeout is rejected", body: "[sleep]\nenabled = true\nidle_timeout = \"-5m\"\n", wantErr: true},
		{name: "an invalid duration is rejected", body: "[sleep]\nenabled = true\nidle_timeout = \"soon\"\n", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			o, err := load(writeConfig(t, testCase.body))
			if testCase.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if o.sleep.enabled != testCase.wantEnabled {
				t.Errorf("enabled = %t, want %t", o.sleep.enabled, testCase.wantEnabled)
			}
			if o.sleep.idleTimeout != testCase.wantTimeout {
				t.Errorf("idleTimeout = %v, want %v", o.sleep.idleTimeout, testCase.wantTimeout)
			}
		})
	}
}

func TestMakeDirs(t *testing.T) {
	dir := t.TempDir()
	o := defaultOpts()
	o.baseDir = dir
	o.deriveDirs()
	o.cfg.SocketsDir = filepath.Join(dir, "run")

	if err := makeDirs(o); err != nil {
		t.Fatal(err)
	}
	if err := makeDirs(o); err != nil {
		t.Fatalf("makeDirs is not repeatable: %v", err)
	}
	for path, want := range map[string]os.FileMode{
		o.cfg.MachinesDir: 0o750,
		o.cfg.SocketsDir:  0o700,
		o.imagesDir:       0o755,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}
