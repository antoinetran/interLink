package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"

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

	req.Header.Set("Content-Type", "application/json")
	log.G(ctx).Debug("Doing request: " + string(req.RequestURI))
	resp, err := DoReq(req)

	if err != nil {
		statusCode := http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(ctx).Error(err)
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		log.G(ctx).Error("HTTP request in error.")
		statusCode := http.StatusInternalServerError
		w.WriteHeader(statusCode)
		ret, err := io.ReadAll(resp.Body)
		if err != nil {
			log.G(ctx).Error("HTTP request in error and could not read body response: see error below.")
			log.G(ctx).Error(err)
			return nil, err
		}
		log.G(ctx).Error("HTTP request in error, body response: " + string(ret))
		_, err = w.Write(ret)
		if err != nil {
			log.G(ctx).Error("HTTP request in error and could not write body response to InterLink Node: see error below.")
			log.G(ctx).Error(err)
			return nil, err
		}
		return nil, fmt.Errorf("call exit status: %d. Body: %s", statusCode, ret)
	}

	w.WriteHeader(resp.StatusCode)
	types.SetDurationSpan(start, span, types.WithHTTPReturnCode(resp.StatusCode))

	if respondWithValues {
		// In this case, we return continuously the values in the w, instead of reading it all. This allows for logs to be followed.
		bodyReader := bufio.NewReader(resp.Body)

		// 4096 is bufio.NewReader default buffer size.
		bufferBytes := make([]byte, 4096)
		// Looping until we get EOF from sidecar.
		for {
			log.G(ctx).Debug("Reading some bytes from InterLink sidecar " + string(req.RequestURI))
			n, err := bodyReader.Read(bufferBytes)
			if err != nil {
				if err == io.EOF {
					// Nothing more to read, we returns nothing because we have already written to w.
					return nil, nil
				} else {
					// Error during read.
					log.G(ctx).Error("Could not read HTTP body: see error below.")
					log.G(ctx).Error(err)
					return nil, err
				}
			}
			log.G(ctx).Debug("Received some bytes from InterLink sidecar content: " + string(bufferBytes[:n]))
			_, err = w.Write(bufferBytes[:n])
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				log.G(ctx).Error(err)
			}
		}
	} else {
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
