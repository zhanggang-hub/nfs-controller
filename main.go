package main

import (
	"context"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"log"
	"nfs-controller/pkg"
)

func main() {
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		inClusterConfig, err := rest.InClusterConfig()
		if err != nil {
			log.Fatalln("can't get config")
		}
		config = inClusterConfig
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalln("can't create client")
	}

	factory := informers.NewSharedInformerFactory(clientset, 0)
	dsinformer := factory.Apps().V1().DaemonSets()
	podinformer := factory.Core().V1().Pods()
	nsinformer := factory.Core().V1().Namespaces()
	newcontroller := pkg.Newcontroller(clientset, dsinformer, podinformer, nsinformer)
	_, err = clientset.AppsV1().DaemonSets("nfs-watch").Get(context.TODO(), "nfs-watch-ds", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		err = newcontroller.DScreate()
		if err != nil {
			log.Println(err)
		}
	}

	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)
	newcontroller.Run(stopCh)
}
