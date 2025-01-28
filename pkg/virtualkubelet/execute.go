package virtualkubelet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	types "github.com/intertwin-eu/interlink/pkg/interlink"
)

const PodPhaseInitialize = "Initializing"

func failedMount(ctx context.Context, failedAndWait *bool, name string, pod *v1.Pod, p *Provider) error {
	*failedAndWait = true
	log.G(ctx).Warning("Unable to find ConfigMap " + name + " for pod " + pod.Name + ". Waiting for it to be initialized")
	if pod.Status.Phase != PodPhaseInitialize {
		pod.Status.Phase = PodPhaseInitialize
		err := p.UpdatePod(ctx, pod)
		if err != nil {
			return err
		}
	}
	return nil

}

func traceExecute(ctx context.Context, pod *v1.Pod, name string, startHTTPCall int64) *trace.Span {
	tracer := otel.Tracer("interlink-service")
	_, spanHTTP := tracer.Start(ctx, name, trace.WithAttributes(
		attribute.String("pod.name", pod.Name),
		attribute.String("pod.namespace", pod.Namespace),
		attribute.String("pod.uid", string(pod.UID)),
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	return &spanHTTP
}

func doRequest(req *http.Request, token string) (*http.Response, error) {
	return doRequestWithClient(req, token, http.DefaultClient)
}

func doRequestWithClient(req *http.Request, token string, httpClient *http.Client) (*http.Response, error) {
	if token != "" {
		req.Header.Add("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	return httpClient.Do(req)
}

func getSidecarEndpoint(ctx context.Context, interLinkURL string, interLinkPort string) string {
	interLinkEndpoint := ""
	log.G(ctx).Info("InterlingURL: ", interLinkURL)
	switch {
	case strings.HasPrefix(interLinkURL, "unix://"):
		interLinkEndpoint = "http://unix"
	case strings.HasPrefix(interLinkURL, "http://"):
		interLinkEndpoint = interLinkURL + ":" + interLinkPort
	case strings.HasPrefix(interLinkURL, "https://"):
		interLinkEndpoint = interLinkURL + ":" + interLinkPort
	default:
		log.G(ctx).Fatal("InterLinkURL URL should either start per unix:// or http(s)://")
	}
	return interLinkEndpoint
}

// PingInterLink pings the InterLink API and returns true if there's an answer. The second return value is given by the answer provided by the API.
func PingInterLink(ctx context.Context, config Config) (bool, int, error) {
	tracer := otel.Tracer("interlink-service")
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)
	log.G(ctx).Info("Pinging: " + interLinkEndpoint + "/pinglink")
	retVal := -1
	req, err := http.NewRequest(http.MethodPost, interLinkEndpoint+"/pinglink", nil)

	if err != nil {
		log.G(ctx).Error(err)
	}

	if config.VKTokenFile != "" {
		token, err := os.ReadFile(config.VKTokenFile) // just pass the file name
		if err != nil {
			log.G(ctx).Error(err)
			return false, retVal, err
		}
		req.Header.Add("Authorization", "Bearer "+string(token))
	}

	startHTTPCall := time.Now().UnixMicro()
	_, spanHTTP := tracer.Start(ctx, "PingHttpCall", trace.WithAttributes(
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "PingInterLink#"+strconv.Itoa(rand.Intn(100000)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		spanHTTP.SetAttributes(attribute.Int("exit.code", http.StatusInternalServerError))
		return false, retVal, err
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		log.G(ctx).Error(err)
		return false, retVal, err
	}

	if resp.StatusCode != http.StatusOK {
		log.G(ctx).Error("server error: " + fmt.Sprint(resp.StatusCode))
		return false, retVal, nil
	}

	return true, resp.StatusCode, nil
}

// updateCacheRequest is called when the VK receives the status of a pod already deleted. It performs a REST call InterLink API to update the cache deleting that pod from the cached structure
func updateCacheRequest(ctx context.Context, config Config, pod v1.Pod, token string) error {
	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.L.Error(err)
		return err
	}

	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, interLinkEndpoint+"/updateCache", reader)
	if err != nil {
		log.L.Error(err)
		return err
	}

	if token != "" {
		req.Header.Add("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	startHTTPCall := time.Now().UnixMicro()
	spanHTTP := traceExecute(ctx, &pod, "UpdateCacheHttpCall", startHTTPCall)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "UpdateCache#"+strconv.Itoa(rand.Intn(100000)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.L.Error(err)
		return err
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, *spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	if resp.StatusCode != http.StatusOK {
		return errors.New("Unexpected error occured while updating InterLink cache. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}

	return err
}

// createRequest performs a REST call to the InterLink API when a Pod is registered to the VK. It Marshals the pod with already retrieved ConfigMaps and Secrets and sends it to InterLink.
// Returns the call response expressed in bytes and/or the first encountered error
func createRequest(ctx context.Context, config Config, pod types.PodCreateRequests, token string) ([]byte, error) {
	tracer := otel.Tracer("interlink-service")
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)

	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, interLinkEndpoint+"/create", reader)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	startHTTPCall := time.Now().UnixMicro()
	_, spanHTTP := tracer.Start(ctx, "CreateHttpCall", trace.WithAttributes(
		attribute.String("pod.name", pod.Pod.Name),
		attribute.String("pod.namespace", pod.Pod.Namespace),
		attribute.String("pod.uid", string(pod.Pod.UID)),
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "CreatePod#"+strconv.Itoa(rand.Intn(100000)))

	resp, err := doRequest(req, token)
	if err != nil {
		return nil, fmt.Errorf("error doing doRequest() in createRequest() log request: %s error: %w", fmt.Sprintf("%#v", req), err)
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("Unexpected error occured while creating Pods. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}
	returnValue, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error doing ReadAll() in createRequest() log request: %s error: %w", fmt.Sprintf("%#v", req), err)
	}

	return returnValue, nil
}

// deleteRequest performs a REST call to the InterLink API when a Pod is deleted from the VK. It Marshals the standard v1.Pod struct and sends it to InterLink.
// Returns the call response expressed in bytes and/or the first encountered error
func deleteRequest(ctx context.Context, config Config, pod *v1.Pod, token string) ([]byte, error) {
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)
	var returnValue []byte
	bodyBytes, err := json.Marshal(pod)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodDelete, interLinkEndpoint+"/delete", reader)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}

	startHTTPCall := time.Now().UnixMicro()
	spanHTTP := traceExecute(ctx, pod, "DeleteHttpCall", startHTTPCall)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "DeletePod#"+strconv.Itoa(rand.Intn(100000)))

	resp, err := doRequest(req, token)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}
	defer resp.Body.Close()

	statusCode := resp.StatusCode
	types.SetDurationSpan(startHTTPCall, *spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))

	if statusCode != http.StatusOK {
		return nil, errors.New("Unexpected error occured while deleting Pods. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}

	returnValue, err = io.ReadAll(resp.Body)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}
	log.G(context.Background()).Info(string(returnValue))
	var response []types.PodStatus
	err = json.Unmarshal(returnValue, &response)
	if err != nil {
		log.G(context.Background()).Error(err)
		return nil, err
	}

	return returnValue, nil
}

// statusRequest performs a REST call to the InterLink API when the VK needs an update on its Pods' status. A Marshalled slice of v1.Pod is sent to the InterLink API,
// to query the below plugin for their status.
// Returns the call response expressed in bytes and/or the first encountered error
func statusRequest(ctx context.Context, config Config, podsList []*v1.Pod, token string) ([]byte, error) {
	tracer := otel.Tracer("interlink-service")

	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)

	bodyBytes, err := json.Marshal(podsList)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, interLinkEndpoint+"/status", reader)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	//  log.L.Println(string(bodyBytes))

	startHTTPCall := time.Now().UnixMicro()
	_, spanHTTP := tracer.Start(ctx, "StatusHttpCall", trace.WithAttributes(
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, "GetStatus#"+strconv.Itoa(rand.Intn(100000)))

	resp, err := doRequest(req, token)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	if resp.StatusCode != http.StatusOK {
		returnValue, err := io.ReadAll(resp.Body)
		if err != nil {
			log.L.Error(err)
			return nil, err
		}
		return nil, errors.New("Unexpected error occured while getting status. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations\n" + string(returnValue))
	}
	returnValue, err := io.ReadAll(resp.Body)
	if err != nil {
		log.L.Error(err)
		return nil, err
	}

	return returnValue, nil
}

// LogRetrieval performs a REST call to the InterLink API when the user ask for a log retrieval. Compared to create/delete/status request, a way smaller struct is marshalled and sent.
// This struct only includes a minimum data set needed to identify the job/container to get the logs from.
// Returns the call response and/or the first encountered error
func LogRetrieval(
	ctx context.Context,
	config Config,
	logsRequest types.LogStruct,
	clientHTTPTransport *http.Transport,
	sessionContext string,
) (io.ReadCloser, error) {
	tracer := otel.Tracer("interlink-service")
	interLinkEndpoint := getSidecarEndpoint(ctx, config.InterlinkURL, config.Interlinkport)

	token := ""

	if config.VKTokenFile != "" {
		b, err := os.ReadFile(config.VKTokenFile) // just pass the file name
		if err != nil {
			log.G(ctx).Fatal(err)
		}
		token = string(b)
	}

	sessionContextMessage := GetSessionContextMessage(sessionContext)

	bodyBytes, err := json.Marshal(logsRequest)
	if err != nil {
		errWithContext := fmt.Errorf(sessionContextMessage+"error during marshalling to JSON the log request: %s. Bodybytes: %s error: %w", fmt.Sprintf("%#v", logsRequest), bodyBytes, err)
		log.G(ctx).Error(errWithContext)
		return nil, errWithContext
	}

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, interLinkEndpoint+"/getLogs", reader)
	if err != nil {
		errWithContext := fmt.Errorf(sessionContextMessage+"error during HTTP request: %s/getLogs %w", interLinkEndpoint, err)
		log.G(ctx).Error(errWithContext)
		return nil, errWithContext
	}

	// log.G(ctx).Println(string(bodyBytes))

	startHTTPCall := time.Now().UnixMicro()
	_, spanHTTP := tracer.Start(ctx, "LogHttpCall", trace.WithAttributes(
		attribute.String("pod.name", logsRequest.PodName),
		attribute.String("pod.namespace", logsRequest.Namespace),
		attribute.String("pod.uid", logsRequest.PodUID),
		attribute.Int64("start.timestamp", startHTTPCall),
	))
	defer spanHTTP.End()
	defer types.SetDurationSpan(startHTTPCall, spanHTTP)

	log.G(ctx).Debug(sessionContextMessage, "before doRequestWithClient()")
	// Add session number for end-to-end from VK to API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, sessionContext)

	clientHTTPTransport.DisableKeepAlives = true
	clientHTTPTransport.MaxIdleConnsPerHost = -1
	var logHTTPClient = &http.Client{Transport: clientHTTPTransport}

	resp, err := doRequestWithClient(req, token, logHTTPClient)
	if err != nil {
		log.G(ctx).Error(err)
		return nil, err
	}
	// resp.body must not be closed because the kubelet needs to consume it! This is the responsability of the caller to close it.
	// Called here https://github.com/virtual-kubelet/virtual-kubelet/blob/v1.11.0/node/api/logs.go#L132
	// defer resp.Body.Close()
	log.G(ctx).Debug(sessionContextMessage, "after doRequestWithClient()")

	types.SetDurationSpan(startHTTPCall, spanHTTP, types.WithHTTPReturnCode(resp.StatusCode))
	if resp.StatusCode != http.StatusOK {
		err = errors.New(sessionContextMessage + "Unexpected error occured while getting logs. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check InterLink's logs for further informations")
	}

	// return io.NopCloser(bufio.NewReader(resp.Body)), err
	return resp.Body, err
}

func remoteExecutionHandleProjectedSource(
	ctx context.Context, p *Provider, pod *v1.Pod, source v1.VolumeProjection, projectedVolume *v1.ConfigMap,
) error {
	//projectedVolume.Name =
	switch {
	case source.ServiceAccountToken != nil:
		/* Case
		   - serviceAccountToken:
		       expirationSeconds: 3600
		       path: token
		*/
		log.G(ctx).Debug("Volume is a projected volume typed serviceAccountToken")

		// Now using TokenRequest API (https://kubernetes.io/docs/reference/kubernetes-api/authentication-resources/token-request-v1/)
		var expirationSeconds int64
		/*
			TODO: honor the expirationSeconds field and implement a rotation.
			if source.ServiceAccountToken.ExpirationSeconds != nil {
				expirationSeconds = *source.ServiceAccountToken.ExpirationSeconds
			} else {
				// If not expiration is set, set to 1h.
				expirationSeconds = 3600
			}
		*/
		// Infinite = 100 years
		expirationSeconds = 100 * 365 * 24 * 3600

		/*
			// Bount it to POD, so that token is deleted if pod is deleted.
			bountObjectRef := &authenticationv1.BoundObjectReference{
				Kind: "Pod",
				UID:  pod.UID,
				Name: pod.Name,
			}
			// Audience is important to be able to use the token outside the cluster. If it does not contain the
			// KubernetesApiAddr, then it will throw an error
			// "Unauthorized" "couldn't get current server API group list: the server has asked for the client to provide credentials"
			tokenRequest := &authenticationv1.TokenRequest{
				Spec: authenticationv1.TokenRequestSpec{
					Audiences: []string{
						"https://kubernetes.default.svc.cluster.local",
						p.config.KubernetesApiAddr,
						p.config.KubernetesApiAddr + ":" + p.config.KubernetesApiPort,
						"https://" + p.config.KubernetesApiAddr,
						"https://" + p.config.KubernetesApiAddr + ":" + p.config.KubernetesApiPort,
					},
					ExpirationSeconds: &expirationSeconds,
					BoundObjectRef:    bountObjectRef,
				},
			}
		*/

		// Audience is important to be able to use the token outside the cluster. If it does not contain the
		// KubernetesApiAddr, then it will throw an error
		// "Unauthorized" "couldn't get current server API group list: the server has asked for the client to provide credentials"
		/*
			p.config.KubernetesApiAddr + ":" + p.config.KubernetesApiPort,
			"https://" + p.config.KubernetesApiAddr,
					"https://" + p.config.KubernetesApiAddr + ":" + p.config.KubernetesApiPort,
		*/
		tokenRequest := &authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				Audiences: []string{
					"https://kubernetes.default.svc.cluster.local",
				},
				ExpirationSeconds: &expirationSeconds,
			},
		}

		log.G(ctx).Debug("Requesting token... token audience: https://kubernetes.default.svc and ", p.config.KubernetesApiAddr)
		tokenRequestResult, err := p.clientSet.CoreV1().ServiceAccounts(pod.Namespace).CreateToken(
			ctx, pod.Spec.ServiceAccountName, tokenRequest, metav1.CreateOptions{})
		if err != nil {
			log.G(ctx).Error("error during token request in RemoteExecution() ", err)
		}
		log.G(ctx).Debug("could get token ", tokenRequestResult.Status.Token)

		// Add found token to result.
		projectedVolume.Data[source.ServiceAccountToken.Path] = tokenRequestResult.Status.Token

	case source.ConfigMap != nil:
		/* Case
		   - configMap:
		       items:
		         - key: ca.crt
		           path: ca.crt
		       name: kube-root-ca.crt
		*/
		for _, item := range source.ConfigMap.Items {
			KUBE_CA_CRT := "kube-root-ca.crt"
			overrideCaCrt := p.config.KubernetesApiCaCrt
			if source.ConfigMap.Name == KUBE_CA_CRT && overrideCaCrt != "" {
				log.G(ctx).Debug("handling special case of Kubernetes API kube-root-ca.crt, override found, using provided ca.crt:, ", overrideCaCrt)
				projectedVolume.Data[item.Path] = overrideCaCrt
			} else {
				log.G(ctx).Warning("using default Kubernetes API kube-root-ca.crt (no override found), but the default one might not be compatible with the subject: ", p.config.KubernetesApiAddr)
				cfgmap, err := p.clientSet.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, source.ConfigMap.Name, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("error during retrieval of ConfigMap %s error: %w", source.ConfigMap.Name, err)
				}
				if value, ok := cfgmap.Data[item.Key]; ok {
					projectedVolume.Data[item.Path] = value
				} else {
					return fmt.Errorf("error during retrieval of key %s of (existing) ConfigMap %s error: %w", item.Key, source.ConfigMap.Name, err)
				}
			}
		}

	case source.DownwardAPI != nil:
		/* Case
		- downwardAPI:
			items:
			- fieldRef:
				apiVersion: v1
				fieldPath: metadata.namespace
				path: namespace
		*/
		// https://kubernetes.io/docs/concepts/workloads/pods/downward-api/
		// See URL doc above, that describe what type of DownwardAPI to expect from volume. For now, only FieldRef is supported.
		// The rest are ignored.
		for _, item := range source.DownwardAPI.Items {
			switch {

			case item.FieldRef != nil:
				switch item.FieldRef.FieldPath {
				case "metadata.name":
					projectedVolume.Data[item.Path] = pod.Name

				case "metadata.namespace":
					projectedVolume.Data[item.Path] = pod.Namespace

				case "metadata.uid":
					projectedVolume.Data[item.Path] = string(pod.UID)

				// TODO implement DownwardAPI annotation and label

				default:
					log.G(ctx).Warningf("in pod %s unsupported DownwardAPI FieldPath %s in InterLink, ignoring this source...", pod.Name, item.FieldRef.FieldPath)
				}

			case item.ResourceFieldRef != nil:
				// TODO implement DownwardAPI resourceFieldRef
				log.G(ctx).Warningf("in pod %s unsupported DownwardAPI resourceFieldRef in InterLink, ignoring this source...", pod.Name)

			default:
				log.G(ctx).Warningf("in pod %s unsupported unknown DownwardAPI in InterLink, ignoring this source...", pod.Name)
			}

		}
	}
	return nil
}

// Adds to pod environment variables related to services. For now, it only concerns Kubernetes API variables, example below:
/*
KUBERNETES_PORT=tcp://10.96.0.1:443
KUBERNETES_SERVICE_PORT=443
KUBERNETES_PORT_443_TCP_ADDR=10.96.0.1
KUBERNETES_PORT_443_TCP_PORT=443
KUBERNETES_PORT_443_TCP_PROTO=tcp
KUBERNETES_PORT_443_TCP=tcp://10.96.0.1:443
KUBERNETES_SERVICE_PORT_HTTPS=443
KUBERNETES_SERVICE_HOST=10.96.0.1
*/
func addKubernetesServicesEnvVars(ctx context.Context, config Config, pod *v1.Pod) {
	appendEnvVar := func(envs *[]v1.EnvVar, name string, value string) {
		envVar := v1.EnvVar{
			Name:  name,
			Value: value,
		}
		log.G(ctx).Debug("in addKubernetesServicesEnvVars in appendEnvVar before add env: len(envs): ", len(*envs))
		*envs = append(*envs, envVar)
		log.G(ctx).Debug("in addKubernetesServicesEnvVars in appendEnvVar after add env: len(envs): ", len(*envs))
	}
	appendEnvVars := func(containersPtr *[]v1.Container, index int) {
		containers := *containersPtr
		container := containers[index]
		envsPtr := &containers[index].Env

		log.G(ctx).Debug("in addKubernetesServicesEnvVars in appendEnvVars before add env: len(container.Env): ", len(container.Env))
		appendEnvVar(envsPtr, "KUBERNETES_PORT", "tcp://"+config.KubernetesApiAddr+":"+config.KubernetesApiPort)
		appendEnvVar(envsPtr, "KUBERNETES_SERVICE_PORT", config.KubernetesApiPort)
		appendEnvVar(envsPtr, "KUBERNETES_PORT_443_TCP_ADDR", config.KubernetesApiAddr)
		appendEnvVar(envsPtr, "KUBERNETES_PORT_443_TCP_PORT", config.KubernetesApiPort)
		appendEnvVar(envsPtr, "KUBERNETES_PORT_443_TCP_PROTO", "tcp")
		appendEnvVar(envsPtr, "KUBERNETES_PORT_443_TCP", "tcp://"+config.KubernetesApiAddr+":"+config.KubernetesApiPort)
		appendEnvVar(envsPtr, "KUBERNETES_SERVICE_PORT_HTTPS", config.KubernetesApiPort)
		appendEnvVar(envsPtr, "KUBERNETES_SERVICE_HOST", config.KubernetesApiAddr)
		log.G(ctx).Debug("in addKubernetesServicesEnvVars in appendEnvVars after add env: len(container.Env): ", len(container.Env))
	}
	if config.KubernetesApiAddr == "" || config.KubernetesApiPort == "" {
		log.G(ctx).Info("InterLink configuration does not contains both KubernetesApiAddr and KubernetesApiPort, so no env var like KUBERNETES_SERVICE_HOST is added.")
		return
	}
	// Warning: loop range copy value, so to modify containers, we must use index instead.
	for i, _ := range pod.Spec.InitContainers {
		log.G(ctx).Debug("in addKubernetesServicesEnvVars in loop init before add env: len(container.Env): ", len(pod.Spec.InitContainers[i].Env))
		appendEnvVars(&pod.Spec.InitContainers, i)
		log.G(ctx).Debug("in addKubernetesServicesEnvVars in loop init after add env: len(container.Env): ", len(pod.Spec.InitContainers[i].Env))
	}
	for i, _ := range pod.Spec.Containers {
		appendEnvVars(&pod.Spec.Containers, i)
	}

	// For debugging purpose only.
	for _, container := range pod.Spec.InitContainers {
		for _, envVar := range container.Env {
			log.G(ctx).Debug("in addKubernetesServicesEnvVars InterLink VK environment variable to pod ", pod.Name, " container: ", container.Name, " env: ", envVar.Name, " value: ", envVar.Value)
		}
	}
	for _, container := range pod.Spec.Containers {
		for _, envVar := range container.Env {
			log.G(ctx).Debug("in addKubernetesServicesEnvVars InterLink VK environment variable to pod ", pod.Name, " container: ", container.Name, " env: ", envVar.Name, " value: ", envVar.Value)
		}
	}
	log.G(ctx).Info("InterLink VK added a set of environment variables (e.g.: KUBERNETES_SERVICE_HOST) to all containers of pod ",
		pod.Name, " k8s addr ", config.KubernetesApiAddr, " k8s port ", config.KubernetesApiPort)
}

func remoteExecutionHandleVolumes(ctx context.Context, p *Provider, pod *v1.Pod, req *types.PodCreateRequests) error {
	startTime := time.Now()

	timeNow := time.Now()
	_, err := p.clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		log.G(ctx).Warning("Deleted Pod before actual creation")
		return nil
	}
	// Sometime the get secret or configmap can fail because it didn't have time to initialize, thus this
	// is not a true failure. We use this flag to wait.
	var failedAndWait bool

	log.G(ctx).Debug("Looking at volumes")
	for _, volume := range pod.Spec.Volumes {
		log.G(ctx).Debug("Looking at volume ", volume)
		for {
			failedAndWait = false
			if timeNow.Sub(startTime).Seconds() < time.Hour.Minutes()*5 {
				switch {
				case volume.ConfigMap != nil:
					cfgmap, err := p.clientSet.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, volume.ConfigMap.Name, metav1.GetOptions{})
					if err != nil {
						err = failedMount(ctx, &failedAndWait, volume.ConfigMap.Name, pod, p)
						if err != nil {
							return err
						}
					} else {
						req.ConfigMaps = append(req.ConfigMaps, *cfgmap)
					}

				case volume.Projected != nil:
					// The service account token uses the projected volume in K8S >= 1.24.

					var projectedVolume v1.ConfigMap
					projectedVolume.Name = volume.Name
					projectedVolume.Data = make(map[string]string)
					log.G(ctx).Debug("Adding to PodCreateRequests the projected volume ", volume.Name)
					req.ProjectedVolumeMaps = append(req.ProjectedVolumeMaps, projectedVolume)

					for _, source := range volume.Projected.Sources {
						err := remoteExecutionHandleProjectedSource(ctx, p, pod, source, &projectedVolume)
						if err != nil {
							return err
						} else {
							failedAndWait = false
						}
						log.G(ctx).Debug("ProjectedVolumeMaps len: ", len(req.ProjectedVolumeMaps))
					}

				case volume.Secret != nil:
					scrt, err := p.clientSet.CoreV1().Secrets(pod.Namespace).Get(ctx, volume.Secret.SecretName, metav1.GetOptions{})
					if err != nil {
						err = failedMount(ctx, &failedAndWait, volume.Secret.SecretName, pod, p)
						if err != nil {
							return err
						}
					} else {
						req.Secrets = append(req.Secrets, *scrt)
					}

				case volume.EmptyDir != nil:
					log.G(ctx).Debugf("empty dir found, nothing to do for volume %s for Pod %s", volume.Name, pod.Name)

				default:
					log.G(ctx).Warningf("ignoring unsupported volume %s for Pod %s", volume.Name, pod.Name)
				}

				if failedAndWait {
					time.Sleep(time.Second)
					continue
				}
				pod.Status.Phase = v1.PodPending
				err = p.UpdatePod(ctx, pod)
				if err != nil {
					return err
				}
				break
			}

			pod.Status.Phase = v1.PodFailed
			pod.Status.Reason = "CFGMaps/Secrets not found"
			for i := range pod.Status.ContainerStatuses {
				pod.Status.ContainerStatuses[i].Ready = false
			}
			err = p.UpdatePod(ctx, pod)
			if err != nil {
				return err
			}
			return errors.New("unable to retrieve ConfigMaps or Secrets. Check logs")
		}
	}
	return nil
}

// RemoteExecution is called by the VK everytime a Pod is being registered or deleted to/from the VK.
// Depending on the mode (CREATE/DELETE), it performs different actions, making different REST calls.
// Note: for the CREATE mode, the function gets stuck up to 5 minutes waiting for every missing ConfigMap/Secret.
// If after 5m they are not still available, the function errors out
func RemoteExecution(ctx context.Context, config Config, p *Provider, pod *v1.Pod, mode int8) error {

	token := ""
	if config.VKTokenFile != "" {
		b, err := os.ReadFile(config.VKTokenFile) // just pass the file name
		if err != nil {
			log.G(ctx).Fatal(err)
			return err
		}
		token = string(b)
	}
	switch mode {
	case CREATE:
		var req types.PodCreateRequests
		var resp types.CreateStruct

		req.Pod = *pod

		err := remoteExecutionHandleVolumes(ctx, p, pod, &req)
		if err != nil {
			return err
		}

		// Adds special Kubernetes env var. Note: the pod provided by VK is "immutable", well it is a copy. In InterLink, we can modify it.
		addKubernetesServicesEnvVars(ctx, config, pod)

		// For debugging purpose only.
		for _, container := range pod.Spec.InitContainers {
			for _, envVar := range container.Env {
				log.G(ctx).Debug("InterLink VK environment variable to pod ", pod.Name, " container: ", container.Name, " env: ", envVar.Name, " value: ", envVar.Value)
			}
		}
		for _, container := range pod.Spec.Containers {
			for _, envVar := range container.Env {
				log.G(ctx).Debug("InterLink VK environment variable to pod ", pod.Name, " container: ", container.Name, " env: ", envVar.Name, " value: ", envVar.Value)
			}
		}
		returnVal, err := createRequest(ctx, config, req, token)
		if err != nil {
			return fmt.Errorf("error doing createRequest() in RemoteExecution() return value %s error detail %s error: %w", returnVal, fmt.Sprintf("%#v", err), err)
		}

		log.G(ctx).Debug("Pod ", pod.Name, " with Job ID ", resp.PodJID, " before json.Unmarshal()")
		// get remote job ID and annotate it into the pod
		err = json.Unmarshal(returnVal, &resp)
		if err != nil {
			return fmt.Errorf("error doing Unmarshal() in RemoteExecution() return value %s error detail %s error: %w", returnVal, fmt.Sprintf("%#v", err), err)
		}

		if string(pod.UID) == resp.PodUID {
			if pod.Annotations == nil {
				pod.Annotations = map[string]string{}
			}
			pod.Annotations["JobID"] = resp.PodJID
		}

		err = p.UpdatePod(ctx, pod)
		if err != nil {
			return err
		}

		log.G(ctx).Info("Pod " + pod.Name + " created successfully and with Job ID " + resp.PodJID)
		log.G(ctx).Debug(string(returnVal))

	case DELETE:
		req := pod
		if pod.Status.Phase != PodPhaseInitialize {
			returnVal, err := deleteRequest(ctx, config, req, token)
			if err != nil {
				return err
			}
			log.G(ctx).Info(string(returnVal))
		}
	}
	return nil
}

// checkPodsStatus is regularly called by the VK itself at regular intervals of time to query InterLink for Pods' status.
// It basically append all available pods registered to the VK to a slice and passes this slice to the statusRequest function.
// After the statusRequest returns a response, this function uses that response to update every Pod and Container status.
func checkPodsStatus(ctx context.Context, p *Provider, podsList []*v1.Pod, token string, config Config) ([]types.PodStatus, error) {
	var ret []types.PodStatus
	// commented out because it's too verbose. uncomment to see all registered pods
	// log.G(ctx).Debug(p.pods)

	// retrieve pod status from remote interlink
	returnVal, err := statusRequest(ctx, config, podsList, token)
	if err != nil {
		return nil, err
	}

	if returnVal != nil {

		err = json.Unmarshal(returnVal, &ret)
		if err != nil {
			errWithContext := fmt.Errorf("error doing Unmarshal() in checkPodsStatus() error detail: %s error: %w", fmt.Sprintf("%#v", err), err)
			return nil, errWithContext
		}

		// if there is a pod status available go ahead to match with the latest state available in etcd
		if podsList != nil {
			for _, podRemoteStatus := range ret {

				log.G(ctx).Debug(fmt.Sprintln("Get status from remote status len: ", len(podRemoteStatus.Containers)))
				// avoid asking for status too early, when etcd as not been updated
				if podRemoteStatus.PodName != "" {

					// get pod reference from cluster etcd
					podRefInCluster, err := p.GetPod(ctx, podRemoteStatus.PodNamespace, podRemoteStatus.PodName)
					if err != nil {
						log.G(ctx).Warning(err)
						continue
					}
					log.G(ctx).Debug(fmt.Sprintln("Get pod from k8s cluster status: ", podRefInCluster.Status.ContainerStatuses))

					// if the PodUID match with the one in etcd we are talking of the same thing. GOOD
					if podRemoteStatus.PodUID == string(podRefInCluster.UID) {
						podRunning := false
						podErrored := false
						podCompleted := false
						failedReason := ""

						// For each container of the pod we check if there is a previous state known by K8s
						for _, containerRemoteStatus := range podRemoteStatus.Containers {
							index := 0
							foundCt := false

							for i, checkedContainer := range podRefInCluster.Status.ContainerStatuses {
								if checkedContainer.Name == containerRemoteStatus.Name {
									foundCt = true
									index = i
								}
							}

							// if it is the first time checking the container, append it to the pod containers, otherwise just update the correct item
							if !foundCt {
								podRefInCluster.Status.ContainerStatuses = append(podRefInCluster.Status.ContainerStatuses, containerRemoteStatus)
							} else {
								podRefInCluster.Status.ContainerStatuses[index] = containerRemoteStatus
							}

							log.G(ctx).Debug(containerRemoteStatus.State.Running)

							// if plugin cannot return any non-terminated container set the status to terminated
							// if the exit code is != 0 get the error  and set error reason + rememeber to set pod to failed
							switch {
							case containerRemoteStatus.State.Terminated != nil:
								log.G(ctx).Debug("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is not running on Plugin side")
								podCompleted = true
								podRefInCluster.Status.ContainerStatuses[index].State.Terminated.Reason = "Completed"
								if containerRemoteStatus.State.Terminated.ExitCode != 0 {
									podErrored = true
									failedReason = "Error: " + strconv.Itoa(int(containerRemoteStatus.State.Terminated.ExitCode))
									podRefInCluster.Status.ContainerStatuses[index].State.Terminated.Reason = failedReason
									log.G(ctx).Error("Container " + containerRemoteStatus.Name + " exited with error: " + strconv.Itoa(int(containerRemoteStatus.State.Terminated.ExitCode)))
								}
							case containerRemoteStatus.State.Waiting != nil:
								log.G(ctx).Info("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is setting up on Sidecar")
								podRunning = true
							case containerRemoteStatus.State.Running != nil:
								podRunning = true
								log.G(ctx).Debug("Pod " + podRemoteStatus.PodName + ": Service " + containerRemoteStatus.Name + " is running on Sidecar")
							}

							// if this is the first time you see a container running/errored/completed, update the status of the pod.
							switch {
							case podRunning && podRefInCluster.Status.Phase != v1.PodRunning:
								podRefInCluster.Status.Phase = v1.PodRunning
								podRefInCluster.Status.Conditions = append(podRefInCluster.Status.Conditions, v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionTrue})
							case podErrored && podRefInCluster.Status.Phase != v1.PodFailed:
								podRefInCluster.Status.Phase = v1.PodFailed
								podRefInCluster.Status.Reason = failedReason
							case podCompleted && podRefInCluster.Status.Phase != v1.PodSucceeded:
								podRefInCluster.Status.Conditions = append(podRefInCluster.Status.Conditions, v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionFalse})
								podRefInCluster.Status.Phase = v1.PodSucceeded
								podRefInCluster.Status.Reason = "Completed"
							}

						}
					} else {

						// if you don't now any UID yet, collect the status and updated the status cache
						list, err := p.clientSet.CoreV1().Pods(podRemoteStatus.PodNamespace).List(ctx, metav1.ListOptions{})
						if err != nil {
							log.G(ctx).Error(err)
							return nil, err
						}

						pods := list.Items

						for _, pod := range pods {
							if string(pod.UID) == podRemoteStatus.PodUID {
								err = updateCacheRequest(ctx, config, pod, token)
								if err != nil {
									log.G(ctx).Error(err)
									continue
								}
							}
						}

					}
				}
			}
			log.G(ctx).Info("No errors while getting statuses")
			log.G(ctx).Debug(ret)
			return nil, nil
		}

	}

	return nil, err
}
