package main

import (
    "flag"
    "fmt"
    "path/filepath"

    corev1 "k8s.io/api/core/v1"
    "k8s.io/client-go/informers"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/tools/cache"
    "k8s.io/client-go/tools/clientcmd"
    "k8s.io/client-go/util/homedir"

    contextbuilder "github.com/PodPulse/podpulse-agent/internal/context"
    "github.com/PodPulse/podpulse-agent/internal/detector"
)

func buildConfig() (*rest.Config, error) {
    config, err := rest.InClusterConfig()
    if err == nil {
        return config, nil
    }

    var kubeconfig *string
    if home := homedir.HomeDir(); home != "" {
        kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "")
    } else {
        kubeconfig = flag.String("kubeconfig", "", "")
    }
    flag.Parse()

    return clientcmd.BuildConfigFromFlags("", *kubeconfig)
}

func main() {
    config, err := buildConfig()
    if err != nil {
        panic(fmt.Sprintf("failed to build kubeconfig: %v", err))
    }

    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        panic(fmt.Sprintf("failed to create clientset: %v", err))
    }

    factory := informers.NewSharedInformerFactory(clientset, 0)

    podInformer   := factory.Core().V1().Pods().Informer()
    podLister     := factory.Core().V1().Pods().Lister()
    eventInformer := factory.Core().V1().Events().Informer()

    cb := contextbuilder.NewOOMContextBuilder()
    d  := detector.New(podLister, cb)

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
    cache.WaitForCacheSync(stopCh, podInformer.HasSynced, eventInformer.HasSynced)

    fmt.Println("[INFO] PodPulse agent started — watching for OOMKilled events")

    <-stopCh
}