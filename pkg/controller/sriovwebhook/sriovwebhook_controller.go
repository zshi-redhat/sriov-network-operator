package sriovwebhook

import (
	"context"
	"os"
	"time"

	render "github.com/openshift/sriov-network-operator/pkg/render"
	corev1 "k8s.io/api/core/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	WEBHOOK_CONFIGMAP_PATH      = "./bindata/manifests/webhook/configmap"
	WEBHOOK_SERVICE_PATH        = "./bindata/manifests/webhook/service"
	SERVICE_CA_CONFIGMAP        = "openshift-service-ca"
	SRIOV_MUTATING_WEBHOOK_NAME = "network-resources-injector-config"
)

var (
	log          = logf.Log.WithName("controller_sriovwebhook")
	ResyncPeriod = 1 * time.Minute
)

// Add creates a new CA ConfigMap Controller and adds it to the Manager.
// The Manager will set fields on the Controller and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileSRIOVWebhook{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("sriovwebhook-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource CA ConfigMap
	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileSRIOVWebhook{}

// ReconcileSRIOVWebhook reconciles a ConfigMap object
type ReconcileSRIOVWebhook struct {
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile updates MutatingWebhookConfiguration CABundle, given from SERVICE_CA_CONFIGMAP
func (r *ReconcileSRIOVWebhook) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling SRIOV webhook")

	ns := os.Getenv("NAMESPACE")
	if request.Namespace != ns || request.Name != SERVICE_CA_CONFIGMAP {
		return reconcile.Result{}, nil
	}

	data := render.MakeRenderData()
	data.Data["Namespace"] = ns
	data.Data["ServiceCAConfigMap"] = SERVICE_CA_CONFIGMAP

	err := r.Apply(WEBHOOK_CONFIGMAP_PATH, &data)
	if err != nil {
		return reconcile.Result{}, err
	}

	caBundleConfigMap := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), request.NamespacedName, caBundleConfigMap)
	if err != nil {
		reqLogger.Error(err, "Couldn't get caBundle ConfigMap")
		return reconcile.Result{}, err
	}

	caBundleData, ok := caBundleConfigMap.Data["service-ca.crt"]
	if !ok {
		return reconcile.Result{}, err
	}

	// Render Webhook manifests
	data = render.MakeRenderData()
	data.Data["Namespace"] = ns
	data.Data["SRIOVMutatingWebhookName"] = SRIOV_MUTATING_WEBHOOK_NAME
	data.Data["NetworkResourcesInjectorImage"] = os.Getenv("NetworkResourcesInjectorImage")
	data.Data["ReleaseVersion"] = os.Getenv("RELEASEVERSION")
	data.Data["CA_BUNDLE"] = []byte(caBundleData)

	err = r.Apply(WEBHOOK_SERVICE_PATH, &data)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// Apply render and updates webhook manifests
func (r *ReconcileSRIOVWebhook) Apply(manifestDir string, d *render.RenderData) error {
	var err error
	objs := []*uns.Unstructured{}
	objs, err = render.RenderDir(manifestDir, d)
	if err != nil {
		log.Error(err, "Fail to render webhook manifests")
		return err
	}

	for _, obj := range objs {
		err = r.client.Update(context.TODO(), obj)
		if err != nil {
			log.Error(err, "Couldn't update webhook config")
			return err
		}
	}
	return nil
}
