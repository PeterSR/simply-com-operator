/*
Copyright 2026 The simply-com-operator Authors.

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

// Command manager runs the simply-com-operator controllers.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"time"

	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	simplycomv1alpha1 "github.com/PeterSR/simply-com-operator/api/v1alpha1"
	"github.com/PeterSR/simply-com-operator/internal/controller"
	"github.com/PeterSR/simply-com-operator/internal/simply"
)

// version is set at build time.
var version = "dev"

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(simplycomv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr, probeAddr      string
		secureMetrics, enableHTTP2  bool
		enableLeaderElection        bool
		ownerID, apiURL, clusterNS  string
		resync, retry, accountCheck time.Duration
		zoneCacheTTL                time.Duration
		apiRPS                      float64
		apiBurst                    int
		showVersion                 bool
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0",
		"Address the metrics endpoint binds to: :8443 for HTTPS, :8080 for HTTP, 0 to disable.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"Serve metrics over HTTPS, with Kubernetes authentication and authorization.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "Allow HTTP/2 on the metrics server.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Address the health probes bind to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Use leader election, so only one replica works at a time.")
	flag.StringVar(&ownerID, "owner-id", "default",
		"Identifies this installation in the ownership marker of the records it manages. Installations "+
			"sharing a Simply.com zone must use different ids.")
	flag.StringVar(&apiURL, "simply-api-url", simply.DefaultBaseURL, "Simply.com API base URL.")
	flag.StringVar(&clusterNS, "cluster-resource-namespace", "",
		"Namespace of ClusterAccount secrets that name none. Defaults to the namespace the operator runs in.")
	flag.DurationVar(&resync, "resync-interval", 10*time.Minute,
		"How often a record in sync is checked again, to undo changes made outside Kubernetes.")
	flag.DurationVar(&retry, "retry-interval", time.Minute,
		"How long to wait after a problem only the user or Simply.com can fix.")
	flag.DurationVar(&accountCheck, "account-check-interval", time.Hour,
		"How often an account's API key is checked again.")
	flag.DurationVar(&zoneCacheTTL, "zone-cache-ttl", 10*time.Second, "How long a zone listing is reused.")
	flag.Float64Var(&apiRPS, "api-rate-limit", 5, "Most Simply.com API requests per second, for the whole process.")
	flag.IntVar(&apiBurst, "api-burst", 10, "Requests allowed above the rate limit in a burst.")
	flag.BoolVar(&showVersion, "version", false, "Print the version and exit.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	if showVersion {
		fmt.Println(version)
		return
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if !controller.ValidOwnerID(ownerID) {
		setupLog.Error(nil, "--owner-id must be 1 to 63 letters, digits, '.', '_' or '-'", "owner-id", ownerID)
		os.Exit(1)
	}
	if clusterNS == "" {
		clusterNS = os.Getenv("POD_NAMESPACE")
	}
	if clusterNS == "" {
		clusterNS = "default"
	}

	var tlsOpts []func(*tls.Config)
	if !enableHTTP2 {
		// HTTP/2 Stream Cancellation and Rapid Reset (GHSA-qppj-fm5r-hxr3, GHSA-4374-p667-p6c8).
		tlsOpts = append(tlsOpts, func(c *tls.Config) { c.NextProtos = []string{"http/1.1"} })
	}
	metricsOpts := metricsserver.Options{BindAddress: metricsAddr, SecureServing: secureMetrics, TLSOpts: tlsOpts}
	if secureMetrics {
		metricsOpts.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                        scheme,
		Metrics:                       metricsOpts,
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                enableLeaderElection,
		LeaderElectionID:              "simply-com-operator.simplycom.petersr.github.io",
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	limiter := rate.NewLimiter(rate.Limit(apiRPS), apiBurst)
	access := &controller.SimplyAccess{
		Reader:                   mgr.GetClient(),
		SecretReader:             mgr.GetAPIReader(),
		ClusterResourceNamespace: clusterNS,
		ZoneCacheTTL:             zoneCacheTTL,
		NewClient: func(apiKey string) *simply.Client {
			return simply.NewClient(apiKey,
				simply.WithBaseURL(apiURL),
				simply.WithRateLimiter(limiter),
				simply.WithUserAgent("simply-com-operator/"+version))
		},
	}

	if err := (&controller.DNSRecordReconciler{
		Client:         mgr.GetClient(),
		Access:         access,
		Recorder:       mgr.GetEventRecorder("simply-com-operator"),
		OwnerID:        ownerID,
		ResyncInterval: resync,
		RetryInterval:  retry,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "dnsrecord")
		os.Exit(1)
	}
	if err := (&controller.AccountReconciler{
		Client:        mgr.GetClient(),
		Access:        access,
		CheckInterval: accountCheck,
		RetryInterval: retry,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "account")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager", "version", version, "ownerID", ownerID)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
