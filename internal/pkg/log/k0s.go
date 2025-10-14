// SPDX-FileCopyrightText: 2023 k0s authors
// SPDX-License-Identifier: Apache-2.0

package log

import (
	"flag"

	"github.com/bombsimon/logrusr/v4"
	cfssllog "github.com/cloudflare/cfssl/log"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

var setKlogVerbosity func(klog.Level)

type Backend interface{}

type ShutdownLoggingFunc func()

func InitLogging() (Backend, ShutdownLoggingFunc) {
	backend, shutdown := installBackend()

	customFormatter := new(logrus.TextFormatter)
	customFormatter.TimestampFormat = "2006-01-02 15:04:05"
	customFormatter.FullTimestamp = true
	logrus.SetFormatter(customFormatter)

	cfssllog.SetLogger((*cfsslAdapter)(logrus.WithField("component", "cfssl")))
	crlog.SetLogger(logrusr.New(logrus.WithField("component", "controller-runtime")))
	grpclog.SetLoggerV2(newGRPCLogger(logrus.WithField("component", "grpc")))

	// var klogFlags flag.FlagSet
	// klog.InitFlags(&klogFlags)
	// klogVerbosity := klogFlags.Lookup("v").Value.(*klog.Level)
	// setKlogVerbosity = func(level klog.Level) {
	// 	_ = klogVerbosity.Set(level.String()) // can only fail if argument is not a klog.Level
	// }
	// klog.SetLogger(logrusr.New(logrus.WithField("component", "klog")))

	SetWarnLevel()

	return backend, shutdown
}

func SetDebugLevel() {
	logrus.SetLevel(logrus.DebugLevel)
	cfssllog.Level = cfssllog.LevelDebug
	// setKlogVerbosity(5)
}

func SetInfoLevel() {
	logrus.SetLevel(logrus.InfoLevel)
	cfssllog.Level = cfssllog.LevelInfo
	// setKlogVerbosity(1)
}

func SetWarnLevel() {
	logrus.SetLevel(logrus.WarnLevel)
	cfssllog.Level = cfssllog.LevelWarning
	// setKlogVerbosity(0)
}
