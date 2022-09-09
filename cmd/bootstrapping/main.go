// Copyright (c) 2022 Contributors to the Eclipse Foundation
//
// See the NOTICE file(s) distributed with this work for additional
// information regarding copyright ownership.
//
// This program and the accompanying materials are made available under the
// terms of the Eclipse Public License 2.0 which is available at
// https://www.eclipse.org/legal/epl-2.0, or the Apache License, Version 2.0
// which is available at https://www.apache.org/licenses/LICENSE-2.0.
//
// SPDX-License-Identifier: EPL-2.0 OR Apache-2.0

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/imdario/mergo"
	"github.com/pkg/errors"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"

	"github.com/eclipse-kanto/suite-connector/config"
	"github.com/eclipse-kanto/suite-connector/flags"
	"github.com/eclipse-kanto/suite-connector/logger"

	conn "github.com/eclipse-kanto/suite-connector/connector"

	bs "github.com/eclipse-kanto/suite-bootstrapping/internal/bootstrapping"
)

const (
	maxRequestFailures = 3
)

var (
	version = "development"
)

// BootstrapUploader holds the bootstrapping process system data.
type BootstrapUploader struct {
	Pub message.Publisher

	Logger watermill.LoggerAdapter

	Agent *bs.Agent

	requestID       string
	requestFailures int
	mu              sync.Mutex
	signalChan      chan os.Signal
}

func (h *BootstrapUploader) newRequestID(ID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.requestID = ID
	return h.requestID
}

func (h *BootstrapUploader) lastRequestID() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.requestID
}

// Connected is called on connection state change.
func (h *BootstrapUploader) Connected(connected bool, err error) {
	if connected {
		go func() {
			if err := h.Agent.CreateBootstrappingFeature(h.Pub); err != nil {
				h.Logger.Error("'Bootstrapping' feature error", err, nil)
			}

			if err := h.Agent.PublishRequest(h.newRequestID(watermill.NewUUID()), h.Pub); err != nil {
				h.Logger.Error("Bootstrapping request error", err, nil)
			}
		}()
	} else {
		h.newRequestID("")
	}
}

// HandleResponseResult manages the responses management result.
func (h *BootstrapUploader) HandleResponseResult(requestID string, succeeded bool) {
	h.requestFailures = h.requestFailures + 1

	h.Logger.Debug("Bootstrapping request result is received", watermill.LogFields{
		"requestId": requestID,
		"succeeded": succeeded,
		"attempt":   h.requestFailures,
	})

	if requestID != h.lastRequestID() {
		return
	}

	if succeeded || h.requestFailures == maxRequestFailures {
		go func() {
			time.Sleep(3 * time.Second)
			h.Logger.Info("Exiting bootstrapping agent", nil)

			h.signalChan <- syscall.SIGINT
		}()

	} else {
		go func() {
			if err := h.Agent.PublishRequest(h.newRequestID(watermill.NewUUID()), h.Pub); err != nil {
				h.Logger.Error("Bootstrapping request error", err, nil)
			}
		}()
	}
}

func mainLoop(s *bs.BootstrapSettings, logger logger.Logger) error {
	honoClient, cleanup, err := config.CreateHubConnection(&s.HubConnectionSettings, true, logger)
	if err != nil {
		return errors.Wrap(err, "cannot create Hub connection")
	}
	defer cleanup()

	defer honoClient.Disconnect()

	router, err := message.NewRouter(message.RouterConfig{}, logger)
	if err != nil {
		return errors.Wrap(err, "failed to create router")
	}
	config.SetupTracing(router, logger)

	honoPub := config.NewHonoPub(logger, honoClient)
	honoSub := conn.NewSubscriber(honoClient, conn.QosAtMostOnce, false, logger, nil)

	signalChannel := make(chan os.Signal, 2)

	uploader := &BootstrapUploader{
		Pub:        honoPub,
		Logger:     logger,
		signalChan: signalChannel,
	}

	agent, err := bs.NewAgent(s, logger, uploader.HandleResponseResult)
	if err != nil {
		return err
	}
	uploader.Agent = agent

	router.AddHandler("bootstrap_handler",
		"command//+/req/#",
		honoSub,
		conn.TopicEmpty,
		honoPub,
		agent.HandleResponse,
	)

	honoClient.AddConnectionListener(uploader)
	defer honoClient.RemoveConnectionListener(uploader)

	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)

	shutdown := func(r *message.Router) error {
		go func() {
			<-r.Running()

			if err := config.HonoConnect(signalChannel, conn.NullPublisher(), honoClient, logger); err != nil {
				if err := r.Close(); err != nil {
					logger.Error("Messages router close failed", err, nil)
				}
				return
			}

			<-signalChannel

			if err := r.Close(); err != nil {
				logger.Error("Messages router close failed", err, nil)
			}
		}()

		return nil
	}
	router.AddPlugin(shutdown)

	if err := router.Run(context.Background()); err != nil {
		return err
	}

	return nil
}

func main() {
	f := flag.NewFlagSet("bootstrapping", flag.ExitOnError)

	def := bs.DefaultSettings()
	cmd := new(bs.BootstrapSettings)
	flags.AddLog(f, &cmd.LogSettings, &def.LogSettings)
	flags.AddHub(f, &cmd.HubConnectionSettings, &def.HubConnectionSettings)

	fConfigFile := flags.AddGlobal(f)

	f.StringVar(&cmd.PreBootstrapFile,
		"preBootstrapFile", "",
		"Pre-bootstrapping `file`",
	)
	f.Var(flags.NewStringSliceV(&cmd.PreBootstrapScript),
		"preBootstrapScript",
		"Pre-bootstrapping `script`, space separated arguments if any",
	)

	f.StringVar(&cmd.PostBootstrapFile,
		"postBootstrapFile", "",
		"Post-bootstrapping `file`",
	)
	f.Var(flags.NewStringSliceV(&cmd.PostBootstrapScript),
		"postBootstrapScript",
		"Post-bootstrapping `script`, space separated arguments if any",
	)

	f.StringVar(&cmd.ProvisioningFile,
		"provisioningFile", def.ProvisioningFile,
		"Provisioning `file` in JSON format",
	)
	f.StringVar(&cmd.BootstrapProvisioningFile,
		"bootstrapProvisioningFile", "",
		"Location where bootstrapping provisioning JSON result is stored",
	)

	f.IntVar(&cmd.ChunkSize,
		"maxChunkSize", def.ChunkSize,
		"Maximum request chunk `size` in bytes",
	)

	if err := flags.Parse(f, os.Args[1:], version, os.Exit); err != nil {
		log.Fatalf("Cannot parse flags: %v", err)
	}

	settings := bs.DefaultSettings()
	if err := config.ReadConfig(*fConfigFile, settings); err != nil {
		log.Fatalf("Cannot parse config: %v", err)
	}

	if err := settings.Validate(); err != nil {
		log.Fatalf("Error on settings validation %v:", err)
	}

	if err := config.ReadProvisioning(settings.ProvisioningFile, &settings.HubConnectionSettings); err != nil {
		log.Fatalf("Cannot parse config: %v", err)
	}

	args := flags.Copy(f)
	if err := mergo.Map(settings, args, mergo.WithOverwriteWithEmptyValue); err != nil {
		log.Fatalf("Cannot process settings: %v", err)
	}

	loggerOut, logger := logger.Setup("bootstrap", &settings.LogSettings)
	defer loggerOut.Close()

	logger.Infof("Starting bootstrapping agent %s", version)
	flags.ConfigCheck(logger, *fConfigFile)

	if err := mainLoop(settings, logger); err != nil {
		logger.Error("Init failure", err, nil)

		loggerOut.Close()

		os.Exit(1)
	}
}
