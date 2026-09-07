package network

import (
	"errors"
	"fmt"
	"net"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

// bpfActivityLoader creates the real kernel objects with cilium/ebpf. It holds
// no state, so one instance serves every VM.
type bpfActivityLoader struct{}

// createMap creates the shared activity hash map with the given capacity.
func (bpfActivityLoader) createMap(capacity uint32) (activityMap, error) {
	spec, err := loadActivity()
	if err != nil {
		return nil, err
	}

	mapSpec := spec.Maps["activity_by_user_id"]
	mapSpec.MaxEntries = capacity
	kernelMap, err := ebpf.NewMap(mapSpec)
	if err != nil {
		return nil, err
	}

	return &bpfActivityMap{kernelMap: kernelMap}, nil
}

// resolveInterfaceIndex reads the current tap0 index inside the VM namespace.
func (bpfActivityLoader) resolveInterfaceIndex(namespacePath string) (int, error) {
	var interfaceIndex int
	err := inNamespace(osNamespaceSyscalls{}, namespacePath, func() error {
		device, err := net.InterfaceByName(tapName)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", tapName, err)
		}
		interfaceIndex = device.Index
		return nil
	})
	return interfaceIndex, err
}

// loadProgram loads one program instance. It rewrites the read-only user ID
// constant and shares the one activity map instead of a per-program map.
func (bpfActivityLoader) loadProgram(userID uint32, shared activityMap) (activityProgram, error) {
	sharedMap, ok := shared.(*bpfActivityMap)
	if !ok {
		return nil, fmt.Errorf("shared map has unexpected type %T", shared)
	}

	spec, err := loadActivity()
	if err != nil {
		return nil, err
	}
	// The spec map keeps the small C capacity. Match it to the shared map, so the
	// replacement passes the compatibility check.
	spec.Maps["activity_by_user_id"].MaxEntries = sharedMap.kernelMap.MaxEntries()
	if err := spec.Variables["virtual_machine_user_id"].Set(userID); err != nil {
		return nil, fmt.Errorf("set user id constant: %w", err)
	}

	var programs activityPrograms
	options := &ebpf.CollectionOptions{
		MapReplacements: map[string]*ebpf.Map{"activity_by_user_id": sharedMap.kernelMap},
	}
	if err := spec.LoadAndAssign(&programs, options); err != nil {
		return nil, err
	}

	return &bpfActivityProgram{program: programs.RecordActivity, syscalls: osNamespaceSyscalls{}}, nil
}

// bpfActivityMap wraps the shared kernel map.
type bpfActivityMap struct {
	kernelMap *ebpf.Map
}

// lookup returns the last monotonic packet time for one VM user ID.
func (handle *bpfActivityMap) lookup(userID uint32) (uint64, bool, error) {
	var value uint64
	err := handle.kernelMap.Lookup(userID, &value)
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return value, true, nil
}

// delete removes the activity value for one released VM user ID. An absent key
// is not an error, because the map may have no entry yet.
func (handle *bpfActivityMap) delete(userID uint32) error {
	if err := handle.kernelMap.Delete(userID); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return err
	}
	return nil
}

// Close releases the shared kernel map.
func (handle *bpfActivityMap) Close() error { return handle.kernelMap.Close() }

// bpfActivityProgram wraps one loaded program instance and its TCX links.
type bpfActivityProgram struct {
	program  *ebpf.Program
	syscalls namespaceSyscalls
	egress   link.Link
}

// attach hooks the tap0 egress path inside the VM network namespace. The egress
// path carries host-to-guest traffic. The guest's own frames arrive on the
// ingress path and are not hooked, so a sleepy VM idles instead of keeping itself
// awake with link-local IPv6 or other housekeeping.
func (handle *bpfActivityProgram) attach(namespacePath string) (int, error) {
	var interfaceIndex int
	err := inNamespace(handle.syscalls, namespacePath, func() error {
		device, err := net.InterfaceByName(tapName)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", tapName, err)
		}
		interfaceIndex = device.Index

		egress, err := link.AttachTCX(link.TCXOptions{
			Program:   handle.program,
			Attach:    ebpf.AttachTCXEgress,
			Interface: interfaceIndex,
		})
		if err != nil {
			return fmt.Errorf("attach TCX egress: %w", err)
		}

		handle.egress = egress
		return nil
	})
	if err != nil {
		return 0, err
	}
	return interfaceIndex, nil
}

// Close releases the TCX link and the program instance.
func (handle *bpfActivityProgram) Close() error {
	var closeErrors []error
	if handle.egress != nil {
		closeErrors = append(closeErrors, handle.egress.Close())
	}
	closeErrors = append(closeErrors, handle.program.Close())
	return errors.Join(closeErrors...)
}
