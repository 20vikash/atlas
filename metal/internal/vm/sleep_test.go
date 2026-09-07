package vm

import (
	"testing"
	"time"
)

func TestNewManagerValidatesSleepConfiguration(t *testing.T) {
	monitor := &fakeNetworkActivityMonitor{}
	cases := []struct {
		name    string
		config  SleepConfig
		monitor NetworkActivityMonitor
		wantErr bool
	}{
		{name: "disabled needs nothing", config: SleepConfig{}, monitor: nil, wantErr: false},
		{name: "enabled needs a timeout", config: SleepConfig{Enabled: true}, monitor: monitor, wantErr: true},
		{name: "enabled needs a positive timeout", config: SleepConfig{Enabled: true, IdleTimeout: -time.Minute}, monitor: monitor, wantErr: true},
		{name: "enabled needs a monitor", config: SleepConfig{Enabled: true, IdleTimeout: time.Minute}, monitor: nil, wantErr: true},
		{name: "enabled with both is accepted", config: SleepConfig{Enabled: true, IdleTimeout: time.Minute}, monitor: monitor, wantErr: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewManager(
				ManagerConfig{MachinesDirectory: t.TempDir(), Sleep: testCase.config},
				ManagerDependencies{
					Runtime:                &fakeRuntime{},
					Network:                &fakeNetwork{},
					Storage:                &fakeStorage{},
					Snapshots:              fakeSnapshots{},
					NetworkActivityMonitor: testCase.monitor,
				},
			)
			if testCase.wantErr != (err != nil) {
				t.Fatalf("error = %v, want error %t", err, testCase.wantErr)
			}
		})
	}
}
