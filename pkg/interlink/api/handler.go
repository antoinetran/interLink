package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/containerd/containerd/log"

	trace "go.opentelemetry.io/otel/trace"

	"github.com/intertwin-eu/interlink/pkg/interlink"
	types "github.com/intertwin-eu/interlink/pkg/interlink"
)

type InterLinkHandler struct {
	Config          interlink.Config
	Ctx             context.Context
	SidecarEndpoint string
	// TODO: http client with TLS
}

func DoReq(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func ReqWithError(
	ctx context.Context,
	req *http.Request,
	w http.ResponseWriter,
	start int64,
	span trace.Span,
	respondWithValues bool,
) ([]byte, error) {
	return ReqWithErrorWithSessionNumber(ctx, req, w, start, span, respondWithValues, 0)
}

func addSessionNumber(req *http.Request, sessionNumber int) {
	req.Header.Set("InterLink-Http-Session", strconv.Itoa(sessionNumber))
}

func ReqWithErrorWithSessionNumber(
	ctx context.Context,
	req *http.Request,
	w http.ResponseWriter,
	start int64,
	span trace.Span,
	respondWithValues bool,
	sessionNumber int,
) ([]byte, error) {
	req.Header.Set("Content-Type", "application/json")
	log.G(ctx).Debug(GetSessionNumberMessage(sessionNumber) + "doing request: " + fmt.Sprintf("%#v", req))

	// Add session number for end-to-end from API to InterLink plugin (eg interlink-slurm-plugin)
	addSessionNumber(req, sessionNumber)

	resp, err := DoReq(req)
	if err != nil {
		statusCode := http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(ctx).Error(err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.G(ctx).Error(GetSessionNumberMessage(sessionNumber) + "HTTP request in error.")
		statusCode := http.StatusInternalServerError
		w.WriteHeader(statusCode)
		ret, err := io.ReadAll(resp.Body)
		if err != nil {
			log.G(ctx).Error(GetSessionNumberMessage(sessionNumber) + "HTTP request in error and could not read body response: see error below.")
			log.G(ctx).Error(err)
			return nil, err
		}
		log.G(ctx).Error(GetSessionNumberMessage(sessionNumber) + "HTTP request in error, body response: " + string(ret))
		_, err = w.Write(ret)
		if err != nil {
			log.G(ctx).Error(GetSessionNumberMessage(sessionNumber) + "HTTP request in error and could not write body response to InterLink Node: see error below.")
			log.G(ctx).Error(err)
			return nil, err
		}
		return nil, fmt.Errorf(GetSessionNumberMessage(sessionNumber)+"call exit status: %d. Body: %s", statusCode, ret)
	}

	log.G(ctx).Debug(GetSessionNumberMessage(sessionNumber) + "writing OK header")
	w.WriteHeader(resp.StatusCode)
	types.SetDurationSpan(start, span, types.WithHTTPReturnCode(resp.StatusCode))

	if respondWithValues {
		log.G(ctx).Debug(GetSessionNumberMessage(sessionNumber) + "reading continuously until EOF")

		// In this case, we return continuously the values in the w, instead of reading it all. This allows for logs to be followed.
		bodyReader := bufio.NewReader(resp.Body)

		// 4096 is bufio.NewReader default buffer size.
		bufferBytes := make([]byte, 4096)

		// Looping until we get EOF from sidecar.
		for {
			log.G(ctx).Debug(GetSessionNumberMessage(sessionNumber) + "Reading some bytes from InterLink sidecar " + string(req.RequestURI))
			n, err := bodyReader.Read(bufferBytes)
			if err != nil {
				if err == io.EOF {
					// Nothing more to read, we returns nothing because we have already written to w.
					log.G(ctx).Debug(GetSessionNumberMessage(sessionNumber) + "EOF body " + string(req.URL.Host))
					return nil, nil
				} else {
					// Error during read.
					w.WriteHeader(http.StatusInternalServerError)
					return nil, fmt.Errorf(GetSessionNumberMessage(sessionNumber)+"Could not read HTTP body: see error %w", err)
				}
			}
			log.G(ctx).Debug(GetSessionNumberMessage(sessionNumber) + "Received some bytes from InterLink sidecar")
			_, err = w.Write(bufferBytes[:n])
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return nil, fmt.Errorf(GetSessionNumberMessage(sessionNumber)+"could not write during ReqWithError() error: %w", err)
			}

			// Flush otherwise it will take time to appear in kubectl logs.
			if f, ok := w.(http.Flusher); ok {
				log.G(ctx).Debug(GetSessionNumberMessage(sessionNumber) + "Wrote some logs, now flushing...")
				f.Flush()
			} else {
				log.G(ctx).Debug(GetSessionNumberMessage(sessionNumber) + "Wrote some logs but could not flush because server does not support Flusher. It means the logs will take time to appear.")
			}

		}
	} else {
		log.G(ctx).Debug(GetSessionNumberMessage(sessionNumber) + "reading all body once for all")
		returnValue, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.G(ctx).Error(err)
			return nil, err
		}
		log.G(ctx).Debug(string(returnValue))
		return returnValue, nil
	}
}
