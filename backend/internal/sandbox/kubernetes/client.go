package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
)

// kubeClient talks to the core/v1 API of one namespace
// It is a REST client with only the core types registered, since the generated clientset registers every API group and doubles what Kubernetes support adds to the binary
type kubeClient struct {
	rc     *rest.RESTClient
	params runtime.ParameterCodec
	ns     string
}

// newKubeClient builds a core/v1 client from a connection configuration
func newKubeClient(cfg *rest.Config, namespace string) (*kubeClient, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	cfg = rest.CopyConfig(cfg)
	cfg.APIPath = "/api"
	cfg.GroupVersion = &corev1.SchemeGroupVersion
	cfg.NegotiatedSerializer = serializer.NewCodecFactory(scheme).WithoutConversion()
	if cfg.UserAgent == "" {
		cfg.UserAgent = "umpteenth"
	}
	rc, err := rest.RESTClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create the Kubernetes client: %w", err)
	}
	return &kubeClient{rc: rc, params: runtime.NewParameterCodec(scheme), ns: namespace}, nil
}

// serverVersion returns the API server's version, which also proves it is reachable
func (k *kubeClient) serverVersion(ctx context.Context) (string, error) {
	body, err := k.rc.Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return "", err
	}
	var v struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", fmt.Errorf("unexpected version response: %w", err)
	}
	return v.GitVersion, nil
}

// get reads one object of the namespace
func (k *kubeClient) get(ctx context.Context, resource, name string, into runtime.Object) error {
	return k.rc.Get().Namespace(k.ns).Resource(resource).Name(name).Do(ctx).Into(into)
}

// list reads the objects of the namespace that match a label selector
func (k *kubeClient) list(ctx context.Context, resource, selector string, into runtime.Object) error {
	return k.rc.Get().Namespace(k.ns).Resource(resource).VersionedParams(&metav1.ListOptions{LabelSelector: selector}, k.params).Do(ctx).Into(into)
}

// create stores a new object in the namespace and reads back what the API server made of it
func (k *kubeClient) create(ctx context.Context, resource string, obj, into runtime.Object) error {
	return k.rc.Post().Namespace(k.ns).Resource(resource).Body(obj).Do(ctx).Into(into)
}

// update replaces an object of the namespace
func (k *kubeClient) update(ctx context.Context, resource, name string, obj, into runtime.Object) error {
	return k.rc.Put().Namespace(k.ns).Resource(resource).Name(name).Body(obj).Do(ctx).Into(into)
}

// delete removes an object of the namespace, with an optional grace period in seconds
func (k *kubeClient) delete(ctx context.Context, resource, name string, grace *int64) error {
	return k.rc.Delete().Namespace(k.ns).Resource(resource).Name(name).Body(&metav1.DeleteOptions{GracePeriodSeconds: grace}).Do(ctx).Error()
}

// getPod reads a pod of the namespace
func (k *kubeClient) getPod(ctx context.Context, name string) (*corev1.Pod, error) {
	pod := &corev1.Pod{}
	return pod, k.get(ctx, "pods", name, pod)
}

// listPods reads the pods of the namespace that match a label selector
func (k *kubeClient) listPods(ctx context.Context, selector string) ([]corev1.Pod, error) {
	list := &corev1.PodList{}
	return list.Items, k.list(ctx, "pods", selector, list)
}

// createPod creates a pod and returns it with its UID
func (k *kubeClient) createPod(ctx context.Context, pod *corev1.Pod) (*corev1.Pod, error) {
	created := &corev1.Pod{}
	return created, k.create(ctx, "pods", pod, created)
}

// logs streams a container's log
func (k *kubeClient) logs(ctx context.Context, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error) {
	return k.rc.Get().Namespace(k.ns).Resource("pods").Name(pod).SubResource("log").VersionedParams(opts, k.params).Stream(ctx)
}

// execURL is the address of an exec into a pod's container
func (k *kubeClient) execURL(pod string, opts *corev1.PodExecOptions) *url.URL {
	return k.rc.Post().Namespace(k.ns).Resource("pods").Name(pod).SubResource("exec").VersionedParams(opts, k.params).URL()
}

// apiServerService reads the ClusterIP and port of the kubernetes Service, which is how pods reach the API server
func (k *kubeClient) apiServerService(ctx context.Context) (*corev1.Service, error) {
	svc := &corev1.Service{}
	return svc, k.rc.Get().Namespace(metav1.NamespaceDefault).Resource("services").Name("kubernetes").Do(ctx).Into(svc)
}
