package main

import (
	"fmt"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/PodPulse/podpulse-agent/internal/collector"
	"github.com/PodPulse/podpulse-agent/internal/config"
	incidentcontext "github.com/PodPulse/podpulse-agent/internal/context"
	"github.com/PodPulse/podpulse-agent/internal/detector"
	"github.com/PodPulse/podpulse-agent/internal/emitter"
)

func buildConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}

	kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func main() {
	appConfig := config.Load()

	k8sConfig, err := buildConfig()
	if err != nil {
		panic(fmt.Sprintf("failed to build kubeconfig: %v", err))
	}

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		panic(fmt.Sprintf("failed to create clientset: %v", err))
	}

	e, err := emitter.New(appConfig.BackendAddr, appConfig.ApiKey)
	if err != nil {
		panic(fmt.Sprintf("failed to create emitter: %v", err))
	}

	factory := informers.NewSharedInformerFactory(clientset, 0)

	podInformer := factory.Core().V1().Pods().Informer()
	podLister := factory.Core().V1().Pods().Lister()
	eventInformer := factory.Core().V1().Events().Informer()
	rsInformer := factory.Apps().V1().ReplicaSets().Informer()
	rsLister := factory.Apps().V1().ReplicaSets().Lister()

	logCollector := collector.New(clientset, 50)
	cb := incidentcontext.NewOOMContextBuilder(logCollector)
	d := detector.New(podLister, rsLister, cb, e)

	eventInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			event, ok := obj.(*corev1.Event)
			if !ok {
				return
			}
			d.OnEvent(event)
		},
	})

	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(old, new interface{}) {
			oldPod, ok1 := old.(*corev1.Pod)
			newPod, ok2 := new.(*corev1.Pod)
			if !ok1 || !ok2 {
				return
			}
			d.OnPodUpdate(oldPod, newPod)
		},
	})

	stopCh := make(chan struct{})
	factory.Start(stopCh)
	cache.WaitForCacheSync(stopCh, podInformer.HasSynced, eventInformer.HasSynced, rsInformer.HasSynced)

	fmt.Printf("[INFO] PodPulse agent started — backend: %s\n", appConfig.BackendAddr)

	<-stopCh
}
