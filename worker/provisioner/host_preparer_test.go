// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package provisioner_test

import (
	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/mutex"
	gitjujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils/clock"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/names.v2"

	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/network"
	coretesting "github.com/juju/juju/testing"
	"github.com/juju/juju/worker/provisioner"
)

type fakePrepareAPI struct {
	*gitjujutesting.Stub
	requestedBridges []network.DeviceToBridge
	reconfigureDelay int
}

var _ provisioner.PrepareAPI = (*fakePrepareAPI)(nil)

func (api *fakePrepareAPI) HostChangesForContainer(tag names.MachineTag) ([]network.DeviceToBridge, int, error) {
	api.Stub.MethodCall(api, "HostChangesForContainer", tag)
	if err := api.Stub.NextErr(); err != nil {
		return nil, 0, err
	}
	return api.requestedBridges, api.reconfigureDelay, nil
}

func (api *fakePrepareAPI) SetHostMachineNetworkConfig(tag names.MachineTag, config []params.NetworkConfig) error {
	api.Stub.MethodCall(api, "SetHostMachineNetworkConfig", tag, config)
	if err := api.Stub.NextErr(); err != nil {
		return err
	}
	return nil
}

type hostPreparerSuite struct {
	*gitjujutesting.Stub
}

var _ = gc.Suite(&hostPreparerSuite{})

func (s *hostPreparerSuite) SetUpTest(c *gc.C) {
	s.Stub = &gitjujutesting.Stub{}
}

type stubReleaser struct {
	*gitjujutesting.Stub
}

func (r *stubReleaser) Release() {
	r.MethodCall(r, "Release")
}

func (s *hostPreparerSuite) acquireStubLock(_ <-chan struct{}) (mutex.Releaser, error) {
	s.Stub.AddCall("AcquireLock")
	if err := s.Stub.NextErr(); err != nil {
		return nil, err
	}
	return &stubReleaser{
		Stub: s.Stub,
	}, nil
}

type stubBridger struct {
	*gitjujutesting.Stub
}

var _ network.Bridger = (*stubBridger)(nil)

func (br *stubBridger) Bridge(devices []network.DeviceToBridge, reconfigureDelay int) error {
	br.Stub.MethodCall(br, "Bridge", devices, reconfigureDelay)
	if err := br.Stub.NextErr(); err != nil {
		return err
	}
	return nil
}

func (s *hostPreparerSuite) createStubBridger() (network.Bridger, error) {
	s.Stub.AddCall("CreateBridger")
	if err := s.Stub.NextErr(); err != nil {
		return nil, err
	}
	return &stubBridger{
		s.Stub,
	}, nil
}

type cannedNetworkObserver struct {
	*gitjujutesting.Stub
	config []params.NetworkConfig
}

func (cno *cannedNetworkObserver) ObserveNetwork() ([]params.NetworkConfig, error) {
	cno.Stub.AddCall("ObserveNetwork")
	if err := cno.Stub.NextErr(); err != nil {
		return nil, err
	}
	return cno.config, nil
}

func (s *hostPreparerSuite) createPreparerParams(bridges []network.DeviceToBridge, observed []params.NetworkConfig) provisioner.HostPreparerParams {
	observer := &cannedNetworkObserver{
		Stub:   s.Stub,
		config: observed,
	}
	return provisioner.HostPreparerParams{
		API: &fakePrepareAPI{
			Stub:             s.Stub,
			requestedBridges: bridges,
		},
		LockName:           "prepare-test-lock",
		AcquireLockFunc:    s.acquireStubLock,
		CreateBridger:      s.createStubBridger,
		ObserveNetworkFunc: observer.ObserveNetwork,
		MachineTag:         names.NewMachineTag("1"),
	}
}

func (s *hostPreparerSuite) createPreparer(bridges []network.DeviceToBridge, observed []params.NetworkConfig) *provisioner.HostPreparer {
	params := s.createPreparerParams(bridges, observed)
	return provisioner.NewHostPreparer(params)
}

func (s *hostPreparerSuite) TestPrepareHostNoChanges(c *gc.C) {
	preparer := s.createPreparer(nil, nil)
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err := preparer.Prepare(containerTag, logger)
	c.Assert(err, jc.ErrorIsNil)
	// If HostChangesForContainer returns nothing to change, then we don't
	// instantiate a Bridger, or do any bridging.
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}})
}

var cannedObservedNetworkConfig = []params.NetworkConfig{{
	DeviceIndex:         0,
	MACAddress:          "aa:bb:cc:dd:ee:ff",
	CIDR:                "127.0.0.1/24",
	MTU:                 1500,
	InterfaceName:       "lo",
	ParentInterfaceName: "",
	InterfaceType:       string(network.LoopbackInterface),
	Disabled:            false,
	NoAutoStart:         false,
	ConfigType:          string(network.ConfigLoopback),
}, {
	DeviceIndex:         1,
	MACAddress:          "bb:cc:dd:ee:ff:00",
	CIDR:                "",
	MTU:                 1500,
	InterfaceName:       "eth0",
	ParentInterfaceName: "br-eth0",
	InterfaceType:       string(network.EthernetInterface),
	Disabled:            false,
	NoAutoStart:         false,
	ConfigType:          string(network.ConfigStatic),
}, {
	DeviceIndex:         2,
	MACAddress:          "bb:cc:dd:ee:ff:00",
	CIDR:                "",
	MTU:                 1500,
	InterfaceName:       "br-eth0",
	ParentInterfaceName: "",
	InterfaceType:       string(network.BridgeInterface),
	Disabled:            false,
	NoAutoStart:         false,
	ConfigType:          string(network.ConfigStatic),
}}

func (s *hostPreparerSuite) TestPrepareHostCreateBridge(c *gc.C) {
	devices := []network.DeviceToBridge{{
		DeviceName: "eth0",
		BridgeName: "br-eth0",
	}}
	preparer := s.createPreparer(devices, cannedObservedNetworkConfig)
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err := preparer.Prepare(containerTag, logger)
	c.Assert(err, jc.ErrorIsNil)
	// This should be the normal flow if there are changes necessary. We read
	// the changes, grab a bridger, then acquire a lock, do the bridging,
	// observe the results, report the results, and release the lock.
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}, {
		FuncName: "CreateBridger",
	}, {
		FuncName: "AcquireLock",
	}, {
		FuncName: "Bridge",
		Args:     []interface{}{devices, 0},
	}, {
		FuncName: "ObserveNetwork",
	}, {
		FuncName: "SetHostMachineNetworkConfig",
		Args:     []interface{}{names.NewMachineTag("1"), cannedObservedNetworkConfig},
	}, {
		FuncName: "Release",
	}})
}

func (s *hostPreparerSuite) TestPrepareHostNothingObserved(c *gc.C) {
	devices := []network.DeviceToBridge{{
		DeviceName: "eth0",
		BridgeName: "br-eth0",
	}}
	observed := []params.NetworkConfig(nil)
	preparer := s.createPreparer(devices, observed)
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err := preparer.Prepare(containerTag, logger)
	c.Assert(err, jc.ErrorIsNil)
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}, {
		FuncName: "CreateBridger",
	}, {
		FuncName: "AcquireLock",
	}, {
		FuncName: "Bridge",
		Args:     []interface{}{devices, 0},
	}, {
		FuncName: "ObserveNetwork",
		// We don't call SetHostMachineNetworkConfig if ObserveNetwork returns nothing
	}, {
		FuncName: "Release",
	}})
}

func (s *hostPreparerSuite) TestPrepareHostChangesUnsupported(c *gc.C) {
	s.Stub.SetErrors(
		errors.NotSupportedf("container address allocation"),
	)
	preparer := s.createPreparer(nil, nil)
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err := preparer.Prepare(containerTag, logger)
	c.Assert(err, gc.ErrorMatches, "unable to setup network: container address allocation not supported")
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}})
}

func (s *hostPreparerSuite) TestPrepareHostNoBridger(c *gc.C) {
	s.Stub.SetErrors(
		nil, // HostChangesForContainer
		errors.New("unable to find python interpreter"), // CreateBridger
	)
	devices := []network.DeviceToBridge{{
		DeviceName: "eth0",
		BridgeName: "br-eth0",
	}}
	preparer := s.createPreparer(devices, nil)
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err := preparer.Prepare(containerTag, logger)
	c.Check(err, gc.ErrorMatches, "unable to find python interpreter")
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}, {
		FuncName: "CreateBridger",
	}})
}

func (s *hostPreparerSuite) TestPrepareHostNoLock(c *gc.C) {
	s.Stub.SetErrors(
		nil,              // HostChangesForContainer
		nil,              // CreateBridger
		mutex.ErrTimeout, // AcquireLock
	)
	devices := []network.DeviceToBridge{{
		DeviceName: "eth0",
		BridgeName: "br-eth0",
	}}
	preparer := s.createPreparer(devices, nil)
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err := preparer.Prepare(containerTag, logger)
	c.Check(err, gc.ErrorMatches, `failed to acquire lock "prepare-test-lock" for bridging: timeout acquiring mutex`)
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}, {
		FuncName: "CreateBridger",
	}, {
		FuncName: "AcquireLock",
	}})
}

func (s *hostPreparerSuite) TestPrepareHostBridgeFailure(c *gc.C) {
	s.Stub.SetErrors(
		nil, // HostChangesForContainer
		nil, // CreateBridger
		nil, // AcquireLock
		errors.New("script invocation error: IOError"), // Bridge
	)
	devices := []network.DeviceToBridge{{
		DeviceName: "eth0",
		BridgeName: "br-eth0",
	}}
	preparer := s.createPreparer(devices, nil)
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err := preparer.Prepare(containerTag, logger)
	c.Check(err, gc.ErrorMatches, `failed to bridge devices: script invocation error: IOError`)
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}, {
		FuncName: "CreateBridger",
	}, {
		FuncName: "AcquireLock",
	}, {
		FuncName: "Bridge",
		Args:     []interface{}{devices, 0},
	}, {
		// We don't observe the network information.
		// TODO(jam): 2017-02-15 This is possibly wrong, we might consider
		// a) Forcibly restarting if Bridge() fails,
		// b) Still observing and reporting our observation so that we at least
		//    know what state we ended up in.
		FuncName: "Release",
	}})
}

func (s *hostPreparerSuite) TestPrepareHostObserveFailure(c *gc.C) {
	s.Stub.SetErrors(
		nil, // HostChangesForContainer
		nil, // CreateBridger
		nil, // AcquireLock
		nil, // BridgeBridgeFailure
		errors.New("cannot get network interfaces: enoent"), // GetObservedNetworkConfig
	)
	devices := []network.DeviceToBridge{{
		DeviceName: "eth0",
		BridgeName: "br-eth0",
	}}
	preparer := s.createPreparer(devices, nil)
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err := preparer.Prepare(containerTag, logger)
	c.Check(err, gc.ErrorMatches, `cannot discover observed network config: cannot get network interfaces: enoent`)
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}, {
		FuncName: "CreateBridger",
	}, {
		FuncName: "AcquireLock",
	}, {
		FuncName: "Bridge",
		Args:     []interface{}{devices, 0},
	}, {
		FuncName: "ObserveNetwork",
	}, {
		// We don't SetHostMachineNetworkConfig, but we still release the lock
		FuncName: "Release",
	}})
}

func (s *hostPreparerSuite) TestPrepareHostObservedFailure(c *gc.C) {
	s.Stub.SetErrors(
		nil, // HostChangesForContainer
		nil, // CreateBridger
		nil, // AcquireLock
		nil, // BridgeBridgeFailure
		nil, // ObserveNetwork
		errors.Unauthorizedf("failure"), // SetHostMachineNetworkConfig
	)
	devices := []network.DeviceToBridge{{
		DeviceName: "eth0",
		BridgeName: "br-eth0",
	}}
	preparer := s.createPreparer(devices, cannedObservedNetworkConfig)
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err := preparer.Prepare(containerTag, logger)
	c.Check(err, gc.ErrorMatches, `failure`)
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}, {
		FuncName: "CreateBridger",
	}, {
		FuncName: "AcquireLock",
	}, {
		FuncName: "Bridge",
		Args:     []interface{}{devices, 0},
	}, {
		FuncName: "ObserveNetwork",
	}, {
		FuncName: "SetHostMachineNetworkConfig",
		Args:     []interface{}{names.NewMachineTag("1"), cannedObservedNetworkConfig},
	}, {
		FuncName: "Release",
	}})
}

func (s *hostPreparerSuite) TestPrepareHostCancel(c *gc.C) {
	devices := []network.DeviceToBridge{{
		DeviceName: "eth0",
		BridgeName: "br-eth0",
	}}
	params := s.createPreparerParams(devices, nil)
	lockName := params.LockName
	ch := make(chan struct{})
	close(ch)
	params.AbortChan = ch
	// This is what the AcquireLock should look like
	params.AcquireLockFunc = func(abort <-chan struct{}) (mutex.Releaser, error) {
		s.Stub.AddCall("AcquireLockFunc")
		spec := mutex.Spec{
			Name:   lockName,
			Clock:  clock.WallClock,
			Delay:  coretesting.LongWait,
			Cancel: abort,
		}
		return mutex.Acquire(spec)
	}
	preparer := provisioner.NewHostPreparer(params)
	// Now we directly acquire the lock, which should cause us to hang inside the lock
	spec := mutex.Spec{
		Name:  lockName,
		Clock: clock.WallClock,
		Delay: coretesting.ShortWait,
	}
	releaser, err := mutex.Acquire(spec)
	c.Assert(err, jc.ErrorIsNil)
	defer releaser.Release()
	// Now when we prepare, we should fail with 'canceled'
	containerTag := names.NewMachineTag("1/lxd/0")
	logger := loggo.GetLogger("prepare-host.test")
	err = preparer.Prepare(containerTag, logger)
	c.Check(err, gc.ErrorMatches, `failed to acquire lock "prepare-test-lock" for bridging: cancelled acquiring mutex`)
	s.Stub.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{containerTag},
	}, {
		FuncName: "CreateBridger",
	}, {
		FuncName: "AcquireLockFunc",
		// Since we couldn't acquire the lock (we were canceled), we don't do
		// the bridging or release the lock.
	}})
}
