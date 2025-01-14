/*
Copyright 2023 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package log

import (
	"flag"

	"github.com/bombsimon/logrusr/v4"
	cfssllog "github.com/cloudflare/cfssl/log"
	"github.com/sirupsen/logrus"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

var klogVerbosity *klog.Level

func InitLogging() {
	customFormatter := new(logrus.TextFormatter)
	customFormatter.TimestampFormat = "2006-01-02 15:04:05"
	customFormatter.FullTimestamp = true
	logrus.SetFormatter(customFormatter)

	cfssllog.SetLogger((*cfsslAdapter)(logrus.WithField("component", "cfssl")))
	crlog.SetLogger(logrusr.New(logrus.WithField("component", "controller-runtime")))

	var klogFlags flag.FlagSet
	klog.InitFlags(&klogFlags)
	klogVerbosity = klogFlags.Lookup("v").Value.(*klog.Level)
	klog.SetLogger(logrusr.New(logrus.WithField("klog-verbosity", klogVerbosity)))

	SetWarnLevel()
}

func SetDebugLevel() {
	logrus.SetLevel(logrus.DebugLevel)
	cfssllog.Level = cfssllog.LevelDebug
	klogVerbosity.Set("5")
}

func SetInfoLevel() {
	logrus.SetLevel(logrus.InfoLevel)
	cfssllog.Level = cfssllog.LevelInfo
	klogVerbosity.Set("2")
}

func SetWarnLevel() {
	logrus.SetLevel(logrus.WarnLevel)
	cfssllog.Level = cfssllog.LevelWarning
	klogVerbosity.Set("1")
}
