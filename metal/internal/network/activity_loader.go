package network

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
)

// bpfActivityLoader creates the real kernel objects with cilium/ebpf. It holds
// no state, so one instance serves every VM.
type bpfActivityLoader struct{}

// createSharedMaps creates the three maps that every VM program shares: the
// activity hash, the wake state hash, and the wake event ring buffer.
func (bpfActivityLoader) createSharedMaps(capacity uint32) (sharedMaps, error) {
	spec, err := loadActivity()
	if err != nil {
		return sharedMaps{}, err
	}

	activity, err := createHashMap(spec, "activity_by_user_id", capacity)
	if err != nil {
		return sharedMaps{}, err
	}
	wakeState, err := createHashMap(spec, "wake_state_by_user_id", capacity)
	if err != nil {
		return sharedMaps{}, errors.Join(err, activity.Close())
	}
	wakeEvents, err := ebpf.NewMap(spec.Maps["wake_events"].Copy())
	if err != nil {
		return sharedMaps{}, errors.Join(err, activity.Close(), wakeState.Close())
	}

	return sharedMaps{
		activity:   &bpfActivityMap{kernelMap: activity},
		wakeState:  &bpfWakeStateMap{kernelMap: wakeState},
		wakeEvents: &bpfWakeEventMap{kernelMap: wakeEvents},
	}, nil
}

// createHashMap creates one hash map from the spec with the given capacity.
func createHashMap(spec *ebpf.CollectionSpec, name string, capacity uint32) (*ebpf.Map, error) {
	mapSpec := spec.Maps[name].Copy()
	mapSpec.MaxEntries = capacity
	return ebpf.NewMap(mapSpec)
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
// constant and shares the three maps instead of the per-program maps.
func (bpfActivityLoader) loadProgram(userID uint32, shared sharedMaps) (activityProgram, error) {
	activityMap, ok := shared.activity.(*bpfActivityMap)
	if !ok {
		return nil, fmt.Errorf("shared activity map has unexpected type %T", shared.activity)
	}
	wakeStateMap, ok := shared.wakeState.(*bpfWakeStateMap)
	if !ok {
		return nil, fmt.Errorf("shared wake state map has unexpected type %T", shared.wakeState)
	}
	wakeEventMap, ok := shared.wakeEvents.(*bpfWakeEventMap)
	if !ok {
		return nil, fmt.Errorf("shared wake event map has unexpected type %T", shared.wakeEvents)
	}

	spec, err := loadActivity()
	if err != nil {
		return nil, err
	}
	// The spec maps keep the small C capacity. Match them to the shared maps, so
	// each replacement passes the compatibility check.
	spec.Maps["activity_by_user_id"].MaxEntries = activityMap.kernelMap.MaxEntries()
	spec.Maps["wake_state_by_user_id"].MaxEntries = wakeStateMap.kernelMap.MaxEntries()
	if err := spec.Variables["virtual_machine_user_id"].Set(userID); err != nil {
		return nil, fmt.Errorf("set user id constant: %w", err)
	}

	var programs activityPrograms
	options := &ebpf.CollectionOptions{
		MapReplacements: map[string]*ebpf.Map{
			"activity_by_user_id":   activityMap.kernelMap,
			"wake_state_by_user_id": wakeStateMap.kernelMap,
			"wake_events":           wakeEventMap.kernelMap,
		},
	}
	if err := spec.LoadAndAssign(&programs, options); err != nil {
		return nil, err
	}

	return &bpfActivityProgram{program: programs.RecordActivity, syscalls: osNamespaceSyscalls{}}, nil
}

// bpfActivityMap wraps the shared activity hash map.
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

// Close releases the shared activity map.
func (handle *bpfActivityMap) Close() error { return handle.kernelMap.Close() }

// bpfWakeStateMap wraps the shared wake state hash map.
type bpfWakeStateMap struct {
	kernelMap *ebpf.Map
}

// setState writes one wake state for a VM user ID.
func (handle *bpfWakeStateMap) setState(userID, state uint32) error {
	return handle.kernelMap.Update(userID, state, ebpf.UpdateAny)
}

// deleteState removes the wake state for one released VM user ID. An absent key
// is not an error.
func (handle *bpfWakeStateMap) deleteState(userID uint32) error {
	if err := handle.kernelMap.Delete(userID); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return err
	}
	return nil
}

// Close releases the shared wake state map.
func (handle *bpfWakeStateMap) Close() error { return handle.kernelMap.Close() }

// bpfWakeEventMap wraps the shared wake event ring buffer map.
type bpfWakeEventMap struct {
	kernelMap *ebpf.Map
}

// newReader opens one ring reader on the shared wake event map.
func (handle *bpfWakeEventMap) newReader() (wakeEventReader, error) {
	reader, err := ringbuf.NewReader(handle.kernelMap)
	if err != nil {
		return nil, err
	}
	return &bpfWakeEventReader{reader: reader}, nil
}

// Close releases the shared wake event map.
func (handle *bpfWakeEventMap) Close() error { return handle.kernelMap.Close() }

// bpfWakeEventReader decodes wake events from the shared ring buffer.
type bpfWakeEventReader struct {
	reader *ringbuf.Reader
}

// read returns the next wake event as a user ID and monotonic packet time. A
// closed reader returns ringbuf.ErrClosed.
func (handle *bpfWakeEventReader) read() (uint32, uint64, error) {
	record, err := handle.reader.Read()
	if err != nil {
		return 0, 0, err
	}
	if len(record.RawSample) < 16 {
		return 0, 0, fmt.Errorf("wake event sample is %d bytes, want 16", len(record.RawSample))
	}
	userID := binary.LittleEndian.Uint32(record.RawSample[0:4])
	packetTime := binary.LittleEndian.Uint64(record.RawSample[8:16])
	return userID, packetTime, nil
}

// Close stops the ring reader and unblocks a pending read.
func (handle *bpfWakeEventReader) Close() error { return handle.reader.Close() }

// bpfActivityProgram wraps one loaded program instance and its TCX links.
type bpfActivityProgram struct {
	program  *ebpf.Program
	syscalls namespaceSyscalls
	egress   link.Link
}

// attach hooks the tap0 egress path inside the VM network namespace. The egress
// path carries host-to-guest traffic. The guest's own frames arrive on the
// ingress path and are not hooked, so the guest's housekeeping does not keep a
// sleepy VM awake with link-local IPv6 or other traffic.
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
