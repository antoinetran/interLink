package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/containerd/containerd/log"

	types "github.com/intertwin-eu/interlink/pkg/interlink"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

func getSessionNumber(r *http.Request) int {
	sessionNumberString := r.Header.Get("InterLink-Http-Session")
	if sessionNumberString == "" {
		sessionNumberString = "0"
	}
	sessionNumber, err := strconv.Atoi(sessionNumberString)
	if err != nil {
		// Do nothing
		return 0
	}
	return sessionNumber
}

func GetSessionNumberMessage(sessionNumber int) string {
	return "HTTP InterLink session " + strconv.Itoa(sessionNumber) + ": "
}

func (h *InterLinkHandler) GetLogsHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	_, span := tracer.Start(h.Ctx, "GetLogsAPI", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer types.SetDurationSpan(start, span)

	sessionNumber := getSessionNumber(r)

	var statusCode int
	log.G(h.Ctx).Info(GetSessionNumberMessage(sessionNumber) + "InterLink: received GetLogs call")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.G(h.Ctx).Fatal(err)
	}

	log.G(h.Ctx).Info(GetSessionNumberMessage(sessionNumber) + "InterLink: unmarshal GetLogs request")
	var req2 types.LogStruct // incoming request. To be used in interlink API. req is directly forwarded to sidecar
	err = json.Unmarshal(bodyBytes, &req2)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}

	span.SetAttributes(
		attribute.String("pod.name", req2.PodName),
		attribute.String("pod.namespace", req2.Namespace),
		attribute.Int("opts.limitbytes", req2.Opts.LimitBytes),
		attribute.Int("opts.since", req2.Opts.SinceSeconds),
		attribute.Int64("opts.sincetime", req2.Opts.SinceTime.UnixMicro()),
		attribute.Int("opts.tail", req2.Opts.Tail),
		attribute.Bool("opts.follow", req2.Opts.Follow),
		attribute.Bool("opts.previous", req2.Opts.Previous),
		attribute.Bool("opts.timestamps", req2.Opts.Timestamps),
	)

	log.G(h.Ctx).Info(GetSessionNumberMessage(sessionNumber)+"InterLink: new GetLogs podUID: now ", req2.PodUID)
	if (req2.Opts.Tail != 0 && req2.Opts.LimitBytes != 0) || (req2.Opts.SinceSeconds != 0 && !req2.Opts.SinceTime.IsZero()) {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)

		if req2.Opts.Tail != 0 && req2.Opts.LimitBytes != 0 {
			_, err = w.Write([]byte("Both Tail and LimitBytes set. Set only one of them"))
			if err != nil {
				log.G(h.Ctx).Error(errors.New(GetSessionNumberMessage(sessionNumber) + "Failed to write to http buffer"))
			}
			return
		}

		_, err = w.Write([]byte("Both SinceSeconds and SinceTime set. Set only one of them"))
		if err != nil {
			log.G(h.Ctx).Error(errors.New(GetSessionNumberMessage(sessionNumber) + "Failed to write to http buffer"))
		}

	}

	log.G(h.Ctx).Info(GetSessionNumberMessage(sessionNumber) + "InterLink: marshal GetLogs request ")

	bodyBytes, err = json.Marshal(req2)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(h.Ctx).Error(err)
		return
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, h.SidecarEndpoint+"/getLogs", reader)
	if err != nil {
		log.G(h.Ctx).Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")
	/*
		var logHttpClient *http.Client

			if req2.Opts.Follow {
				// Warning, these headers are not sent until a WriteHeader(), that should happen hopefully before the default HTTP request timeout of 30s.
				// If the headers are not sent before 30s, the connection will break.
				// Also response headers should be sent before WriteHeader, because if after, they are ignored.
				log.G(h.Ctx).Debug(GetSessionNumberMessage(sessionNumber) + "adding HTTP streaming headers for keep-alive and chunk")
				w.Header().Set("Connection", "Keep-Alive")
				w.Header().Set("Keep-Alive", "timeout=120")
				w.Header().Set("Transfer-Encoding", "chunked")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				log.G(h.Ctx).Info(GetSessionNumberMessage(sessionNumber) + "InterLink: logs in follow mode, setting infinite timeout for Http Client.")
				logHttpClient = &http.Client{
					// For log in following mode, we request infinity timeout.
					Timeout: 0 * time.Second,
				}
			} else {
				logHttpClient = http.DefaultClient
			}
	*/

	/*
		var logHttpClient = &http.Client{
			//Timeout: 0 * time.Second,
			Transport: &http.Transport{
				DisableKeepAlives:   true,
				MaxIdleConnsPerHost: -1,
				// Gets the insecure flag from default client.
				//TLSClientConfig: http.DefaultTransport.(*http.Transport).TLSClientConfig,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
	*/

	logTransport := http.DefaultTransport.(*http.Transport).Clone()
	logTransport.DisableKeepAlives = true
	logTransport.MaxIdleConnsPerHost = -1
	var logHttpClient = &http.Client{Transport: logTransport}

	log.G(h.Ctx).Debug(GetSessionNumberMessage(sessionNumber) + "Custom HttpClient for log with Keep-Alive disabled + tls: " + strconv.FormatBool(logHttpClient.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify))

	log.G(h.Ctx).Info(GetSessionNumberMessage(sessionNumber) + "InterLink: forwarding GetLogs call to sidecar")
	_, err = ReqWithErrorComplex(h.Ctx, req, w, start, span, true, false, sessionNumber, logHttpClient)
	if err != nil {
		log.L.Error(err)
		return
	}

}
