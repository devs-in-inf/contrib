package gin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/project-flogo/core/data/metadata"
	"github.com/project-flogo/core/support/log"
	"github.com/project-flogo/core/trigger"

	contrib "github.com/project-flogo/contrib/trigger/rest"
)

var ginMetadata = trigger.NewMetadata(&contrib.Settings{}, &contrib.HandlerSettings{}, &contrib.Output{}, &contrib.Reply{})

func init() {
	_ = trigger.Register(&Trigger{}, &Factory{})
}

type Trigger struct {
	id       string
	settings *contrib.Settings
	server   *Server
	logger   log.Logger
}

func (t *Trigger) Initialize(ctx trigger.InitContext) error {
	t.logger = ctx.Logger()
	addr := ":" + strconv.Itoa(t.settings.Port)

	// config := cors.DefaultConfig()
	router := gin.Default()
	// router.Use(cors.New(config))

	for _, handler := range ctx.GetHandlers() {
		s := &contrib.HandlerSettings{}
		err := metadata.MapToStruct(handler.Settings(), s, true)
		if err != nil {
			return err
		}

		method := s.Method
		path := s.Path

		t.logger.Debugf("Registering handler [%s: %s]", method, path)

		router.Handle(method, path, newGinHandler(t, strings.ToUpper(method), handler))
	}

	t.logger.Debugf("Configured on port %d", t.settings.Port)

	var opts []Option

	if t.settings.EnableTLS {
		opts = append(opts, TLS(t.settings.CertFile, t.settings.KeyFile))
	}

	server, err := NewServer(addr, router, opts...)
	if err != nil {
		return err
	}

	t.server = server

	return nil
}

func (t *Trigger) Start() error {
	return t.server.Start()
}

// Stop implements util.Managed.Stop
func (t *Trigger) Stop() error {
	return t.server.Stop()
}

type Factory struct {
}

func (f *Factory) Metadata() *trigger.Metadata {
	return ginMetadata
}

func (f *Factory) New(config *trigger.Config) (trigger.Trigger, error) {
	s := &contrib.Settings{}
	err := metadata.MapToStruct(config.Settings, s, true)
	if err != nil {
		return nil, err
	}

	return &Trigger{id: config.Id, settings: s}, nil
}

func newGinHandler(rt *Trigger, method string, handler trigger.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := rt.logger

		logger.Debugf("Received request for id '%s'", rt.id)

		out := &contrib.Output{}
		out.Method = method

		out.PathParams = make(map[string]string)
		for _, param := range c.Params {
			out.PathParams[param.Key] = param.Value
		}

		queryValues := c.Request.URL.Query()
		out.QueryParams = make(map[string]string, len(queryValues))
		out.Headers = make(map[string]string, len(c.Request.Header))

		for key, value := range c.Request.Header {
			out.Headers[key] = strings.Join(value, ",")
		}

		for key, value := range queryValues {
			out.QueryParams[key] = strings.Join(value, ",")
		}

		contentType := c.Request.Header.Get("Content-Type")
		switch contentType {
		case "application/x-www-form-urlencoded":
			buf := new(bytes.Buffer)
			_, err := buf.ReadFrom(c.Request.Body)
			if err != nil {
				logger.Debugf("Error reading body: %s", err.Error())
				http.Error(c.Writer, err.Error(), http.StatusBadRequest)
				return
			}

			s := buf.String()
			m, err := url.ParseQuery(s)
			if err != nil {
				logger.Debugf("Error parsing query string: %s", err.Error())
				http.Error(c.Writer, err.Error(), http.StatusBadRequest)
				return
			}

			content := make(map[string]interface{}, 0)
			for key, val := range m {
				if len(val) == 1 {
					content[key] = val[0]
				} else {
					content[key] = val[0]
				}
			}

			out.Content = content
		case "application/json":
			var content interface{}
			err := json.NewDecoder(c.Request.Body).Decode(&content)
			if err != nil {
				switch {
				case err == io.EOF:
				default:
					logger.Debugf("Error parsing json body: %s", err.Error())
					http.Error(c.Writer, err.Error(), http.StatusBadRequest)
					return
				}
			}
			out.Content = content
		default:
			b, err := io.ReadAll(c.Request.Body)
			if err != nil {
				logger.Debugf("Error reading body: %s", err.Error())
				http.Error(c.Writer, err.Error(), http.StatusBadRequest)
				return
			}
			out.Content = string(b)
			return
		}

		results, err := handler.Handle(context.Background(), out)
		if err != nil {
			logger.Debugf("Error handling request: %s", err.Error())
			http.Error(c.Writer, err.Error(), http.StatusBadRequest)
			return
		}

		if logger.TraceEnabled() {
			logger.Tracef("Action Results: %#v", results)
		}

		reply := &contrib.Reply{}
		err = reply.FromMap(results)
		if err != nil {
			logger.Debugf("Error mapping results: %s", err.Error())
			http.Error(c.Writer, err.Error(), http.StatusBadRequest)
			return
		}

		// add response headers
		if len(reply.Headers) > 0 {
			if logger.TraceEnabled() {
				logger.Tracef("Adding Headers")
			}

			for key, value := range reply.Headers {
				c.Request.Header.Set(key, value)
			}
		}

		if reply.Code == 0 {
			reply.Code = http.StatusOK
		}

		if reply.Data != nil {

			if logger.DebugEnabled() {
				logger.Debugf("The http reply code is: %d", reply.Code)
				logger.Debugf("The http reply data is: %#v", reply.Data)
			}

			switch t := reply.Data.(type) {
			case string:
				var v interface{}
				err := json.Unmarshal([]byte(t), &v)
				if err != nil {
					//Not a json
					c.Writer.Header().Set("Content-Type", "text/plain; charset=UTF-8")
				} else {
					//Json
					c.Writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
				}

				c.Writer.WriteHeader(reply.Code)
				_, err = c.Writer.Write([]byte(t))
				if err != nil {
					logger.Debugf("Error writing body: %s", err.Error())
				}
				return
			default:
				c.Writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
				c.Writer.WriteHeader(reply.Code)
				if err := json.NewEncoder(c.Writer).Encode(reply.Data); err != nil {
					logger.Debugf("Error encoding json reply: %s", err.Error())
				}
				return
			}
		}

		logger.Debugf("The reply http code is: %d", reply.Code)
		c.Writer.WriteHeader(reply.Code)
	}

}
