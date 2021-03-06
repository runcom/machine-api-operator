package vsphere

// This is a thin layer to implement the machine actuator interface with cloud provider details.
// The lifetime of scope and reconciler is a machine actuator operation.
import (
	"context"
	"fmt"
	"time"

	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	scopeFailFmt        = "%s: failed to create scope for machine: %v"
	reconcilerFailFmt   = "%s: reconciler failed to %s machine: %w"
	createEventAction   = "Create"
	updateEventAction   = "Update"
	deleteEventAction   = "Delete"
	noEventAction       = ""
	requeueAfterSeconds = 20
)

// Actuator is responsible for performing machine reconciliation.
type Actuator struct {
	client        runtimeclient.Client
	apiReader     runtimeclient.Reader
	eventRecorder record.EventRecorder
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	Client        runtimeclient.Client
	APIReader     runtimeclient.Reader
	EventRecorder record.EventRecorder
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		client:        params.Client,
		apiReader:     params.APIReader,
		eventRecorder: params.EventRecorder,
	}
}

// Set corresponding event based on error. It also returns the original error
// for convenience, so callers can do "return handleMachineError(...)".
func (a *Actuator) handleMachineError(machine *machinev1.Machine, err error, eventAction string) error {
	klog.Errorf("%v error: %v", machine.GetName(), err)
	if eventAction != noEventAction {
		a.eventRecorder.Eventf(machine, corev1.EventTypeWarning, "Failed"+eventAction, "%v", err)
	}
	return err
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, machine *machinev1.Machine) error {
	klog.Infof("%s: actuator creating machine", machine.GetName())
	scope, err := newMachineScope(machineScopeParams{
		Context:   ctx,
		client:    a.client,
		machine:   machine,
		apiReader: a.apiReader,
	})
	if err != nil {
		fmtErr := fmt.Errorf(scopeFailFmt, machine.GetName(), err)
		return a.handleMachineError(machine, fmtErr, createEventAction)
	}
	if err := newReconciler(scope).create(); err != nil {
		if err := scope.PatchMachine(); err != nil {
			return err
		}
		fmtErr := fmt.Errorf(reconcilerFailFmt, machine.GetName(), createEventAction, err)
		return a.handleMachineError(machine, fmtErr, createEventAction)
	}
	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, createEventAction, "Created Machine %v", machine.GetName())

	if err := scope.PatchMachine(); err != nil {
		return err
	}
	// Return a requeue after error to add a brief delay between reconciles
	// otherwise we might get a stale object from cache without a taskID and
	// issue a double create.  This will not actually result in a 20 second delay
	// in most cases as the machine should have been patched and the corresponding
	// informer event will result in the machine being reconciled sooner. This
	// ensures that we're reconciling with the latest patched machine object.
	return &machinecontroller.RequeueAfterError{RequeueAfter: requeueAfterSeconds * time.Second}
}

func (a *Actuator) Exists(ctx context.Context, machine *machinev1.Machine) (bool, error) {
	klog.Infof("%s: actuator checking if machine exists", machine.GetName())
	scope, err := newMachineScope(machineScopeParams{
		Context:   ctx,
		client:    a.client,
		machine:   machine,
		apiReader: a.apiReader,
	})
	if err != nil {
		return false, fmt.Errorf(scopeFailFmt, machine.GetName(), err)
	}
	return newReconciler(scope).exists()
}

func (a *Actuator) Update(ctx context.Context, machine *machinev1.Machine) error {
	klog.Infof("%s: actuator updating machine", machine.GetName())
	scope, err := newMachineScope(machineScopeParams{
		Context:   ctx,
		client:    a.client,
		machine:   machine,
		apiReader: a.apiReader,
	})
	if err != nil {
		fmtErr := fmt.Errorf(scopeFailFmt, machine.GetName(), err)
		return a.handleMachineError(machine, fmtErr, updateEventAction)
	}
	if err := newReconciler(scope).update(); err != nil {
		// Update machine and machine status in case it was modified
		if err := scope.PatchMachine(); err != nil {
			return err
		}
		fmtErr := fmt.Errorf(reconcilerFailFmt, machine.GetName(), updateEventAction, err)
		return a.handleMachineError(machine, fmtErr, updateEventAction)
	}
	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, updateEventAction, "Updated Machine %v", machine.GetName())
	return scope.PatchMachine()
}

func (a *Actuator) Delete(ctx context.Context, machine *machinev1.Machine) error {
	klog.Infof("%s: actuator deleting machine", machine.GetName())
	scope, err := newMachineScope(machineScopeParams{
		Context:   ctx,
		client:    a.client,
		machine:   machine,
		apiReader: a.apiReader,
	})
	if err != nil {
		fmtErr := fmt.Errorf(scopeFailFmt, machine.GetName(), err)
		return a.handleMachineError(machine, fmtErr, deleteEventAction)
	}
	if err := newReconciler(scope).delete(); err != nil {
		if err := scope.PatchMachine(); err != nil {
			return err
		}
		fmtErr := fmt.Errorf(reconcilerFailFmt, machine.GetName(), deleteEventAction, err)
		return a.handleMachineError(machine, fmtErr, deleteEventAction)
	}
	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, deleteEventAction, "Deleted machine %v", machine.GetName())
	return scope.PatchMachine()
}
