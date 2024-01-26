package pkg

import (
	"context"
	"fmt"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	dsinformer "k8s.io/client-go/informers/apps/v1"
	coreinformer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	appslist "k8s.io/client-go/listers/apps/v1"
	corelist "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"log"
	"strconv"
	"time"
)

var count int

type controller struct {
	client  *kubernetes.Clientset
	dslist  appslist.DaemonSetLister
	podlist corelist.PodLister
	queue   workqueue.RateLimitingInterface
}

// 字符串转换为资源单位
func resourceQuantityFromStr(capacity string) resource.Quantity {
	// 将字符串转换为字节数 10进制转换为64位整数
	bytes, err := strconv.ParseInt(capacity, 10, 64)
	if err != nil {
		fmt.Println("无法将容量字符串转换为字节数:", err)
		return resource.MustParse("0")
	}
	// 转换为合适的资源单位（例如，1Gi 转换为 1 * resource.Gibibyte）
	quantity := resource.NewQuantity(bytes, resource.BinarySI)
	return *quantity
}

//func (c *controller) dsupdate(oldobj interface{}, newobj interface{}) {
//	oldds := oldobj.(*apps.DaemonSet)
//	newds := newobj.(*apps.DaemonSet)
//	if oldds.Status.NumberReady != newds.Status.NumberReady {
//		c.enqueue(newobj)
//	}
//	return
//}

func (c *controller) DScreate() error {

	_, err := c.dslist.DaemonSets("nfs-watch").Get("nfs-watch-ds")
	if err != nil && errors.IsNotFound(err) {
		ns := c.namespace()
		_, err := c.client.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
		if err != nil {
			log.Println(err)
			return err
		}
		pv := c.pvcreate()
		_, err = c.client.CoreV1().PersistentVolumes().Create(context.TODO(), pv, metav1.CreateOptions{})
		if err != nil {
			log.Println(err)
			return err
		}
		pvc := c.pvccreate()
		_, err = c.client.CoreV1().PersistentVolumeClaims("nfs-watch").Create(context.TODO(), pvc, metav1.CreateOptions{})
		if err != nil {
			log.Println(err)
			return err
		}
		nfsds := c.nfsdscreate()
		_, err = c.client.AppsV1().DaemonSets("nfs-watch").Create(context.TODO(), nfsds, metav1.CreateOptions{})
		if err != nil {
			log.Println(err)
			return err
		}
	}
	return nil
}

func (c *controller) namespace() *core.Namespace {
	ns := core.Namespace{}
	ns.Name = "nfs-watch"
	return &ns
}

func (c *controller) pvcreate() *core.PersistentVolume {
	pv := core.PersistentVolume{}
	pv.Name = "nfs-watch-pv"
	pv.Spec = core.PersistentVolumeSpec{
		Capacity: map[core.ResourceName]resource.Quantity{
			core.ResourceStorage: resourceQuantityFromStr("1"),
		},
		MountOptions:                  []string{"soft", "intr", "timeo=2", "retry=2"},
		PersistentVolumeReclaimPolicy: "Retain",
		StorageClassName:              "nfs-pro-class",
		AccessModes: []core.PersistentVolumeAccessMode{
			"ReadWriteMany",
		},
		PersistentVolumeSource: core.PersistentVolumeSource{
			NFS: &core.NFSVolumeSource{
				Path:   "/data/nfsdata",
				Server: "10.182.0.xx",
			},
		},
	}
	return &pv
}

func (c *controller) pvccreate() *core.PersistentVolumeClaim {
	sc := "nfs-pro-class"
	pvc := core.PersistentVolumeClaim{}
	pvc.Name = "nfs-watch-pvc"
	pvc.Namespace = "nfs-watch"
	pvc.Spec = core.PersistentVolumeClaimSpec{
		StorageClassName: &sc,
		AccessModes: []core.PersistentVolumeAccessMode{
			"ReadWriteMany",
		},
		Resources: core.VolumeResourceRequirements{
			Requests: map[core.ResourceName]resource.Quantity{
				core.ResourceStorage: resourceQuantityFromStr("1"),
			},
		},
	}
	return &pvc
}

func (c *controller) nfsdscreate() *apps.DaemonSet {
	ds := apps.DaemonSet{}
	ds.Name = "nfs-watch-ds"
	ds.Annotations = map[string]string{
		"nfs-node-watch": "true",
	}
	ds.Namespace = "nfs-watch"
	ds.Spec = apps.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"name": "nfs-watch",
			},
		},
		Template: core.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"nfs-node-watch": "true",
				},
				Labels: map[string]string{
					"name": "nfs-watch",
				},
			},
			Spec: core.PodSpec{
				Tolerations: []core.Toleration{
					{
						Operator: core.TolerationOpExists,
					},
				},
				Affinity: &core.Affinity{
					NodeAffinity: &core.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &core.NodeSelector{
							NodeSelectorTerms: []core.NodeSelectorTerm{
								{
									MatchExpressions: []core.NodeSelectorRequirement{
										{
											Key:      "node-role.kubernetes.io/node",
											Operator: "Exists",
										},
									},
								},
							},
						},
					},
				},
				Containers: []core.Container{
					{
						Name:  "nfs-watch-con",
						Image: "nfs-check:v1.1",
						Command: []string{
							"sh",
							"-c",
							"while true;do for i in {1..10};do echo $i > /data/nfs-test/test.txt && sleep 1;if [ $? -eq 1 ];then exit 1;fi;done;done",
						},
						VolumeMounts: []core.VolumeMount{
							{
								Name:      "nfs-watch",
								MountPath: "/data/nfs-test",
							},
						},
					},
				},
				Volumes: []core.Volume{
					{
						Name: "nfs-watch",
						VolumeSource: core.VolumeSource{
							PersistentVolumeClaim: &core.PersistentVolumeClaimVolumeSource{
								ClaimName: "nfs-watch-pvc",
							},
						},
					},
				},
			},
		},
	}
	return &ds
}

func (c *controller) syncnfs(key string) ([]string, error) {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	ns := []string{namespace, name}

	//ds副本数
	//dsget, err := c.dslist.DaemonSets(namespace).Get(name)
	//_, ok := dsget.GetAnnotations()["nfs-node-watch"]
	//if ok && !errors.IsNotFound(err) {
	//	fmt.Println("ds副本数变动,nfs可能有异常,请查看")
	//	//发邮件
	//}
	//pod内容器变化

	if namespace != "nfs-watch" {
		return nil, nil
	}
	podget, err := c.podlist.Pods(namespace).Get(name)
	if errors.IsNotFound(err) {
		log.Println(err)
		return nil, err
	}
	_, ok := podget.GetAnnotations()["nfs-node-watch"]
	if ok {
		podphase := podget.Status.Phase
		if podphase == "Pending" {
			return nil, nil
		}
		containerready := podget.Status.ContainerStatuses[0].Ready
		if !containerready && podphase == "Running" {
			taint := &core.Taint{
				Key:    "nfs-client-mount-error",  // 污点的键，可以根据需要进行自定义设置
				Value:  "true",                    // 污点的值，可以根据需要进行自定义设置
				Effect: core.TaintEffectNoExecute, // 设置污点效果为NoSchedule，可以根据需要进行自定义设置其他效果，如Evict（驱逐）等。
			}
			nodeget, err := c.client.CoreV1().Nodes().Get(context.TODO(), podget.Spec.NodeName, metav1.GetOptions{})
			if err != nil {
				log.Println("node is not found--")
				return nil, err
			}
			//检测节点是否有驱逐污点,节点没污点直接打污点，有污点的话进行判断
			nodetaint := nodeget.Spec.Taints
			if nodetaint == nil {
				nodeget.Spec.Taints = append(nodetaint, *taint)
				_, err = c.client.CoreV1().Nodes().Update(context.TODO(), nodeget, metav1.UpdateOptions{})
				if err != nil {
					log.Println("node更新失败")
					return nil, err
				}
				fmt.Println("节点污点更新添加成功")
				count++
				return nil, nil
			} else if nodetaint != nil {
				for _, i := range nodetaint {
					if i.Effect == "NoExecute" {
						return nil, nil
					} else if i.Key == "nfs-client-mount-error" {
						return nil, nil
					}
				}
				nodeget.Spec.Taints = append(nodetaint, *taint)
				_, err = c.client.CoreV1().Nodes().Update(context.TODO(), nodeget, metav1.UpdateOptions{})
				if err != nil {
					log.Println("node更新失败")
					return nil, err
				}
				fmt.Println("节点污点更新添加成功")
				count++
				return nil, nil
			}

		} else if containerready && podphase == "Running" {
			return ns, nil
		}
	}

	return nil, nil
}
func (c *controller) checknode(ns []string) {
	podget, err := c.podlist.Pods(ns[0]).Get(ns[1])
	if err != nil {
		log.Println(err)
		return
	}
	podphase := podget.Status.Phase
	containerready := podget.Status.ContainerStatuses[0].Ready
	if containerready && podphase == "Running" {
		nodeget, err := c.client.CoreV1().Nodes().Get(context.TODO(), podget.Spec.NodeName, metav1.GetOptions{})
		if err != nil {
			log.Println("node is not found--")
			return
		}
		//检测节点是否有其他驱逐污点
		nodetaint := nodeget.Spec.Taints
		for i, o := range nodetaint {
			if o.Key == "nfs-client-mount-error" && len(nodetaint) > 1 {
				taintsbefor := nodetaint[:i]
				taintsafter := nodetaint[i+1:]
				//...解压缩切片
				nodeget.Spec.Taints = append(taintsbefor, taintsafter...)
				_, err = c.client.CoreV1().Nodes().Update(context.TODO(), nodeget, metav1.UpdateOptions{})
				if err != nil {
					log.Println("node更新失败")
					return
				}
				count--
				fmt.Println("节点污点更新去除成功")

			} else if o.Key == "nfs-client-mount-error" && len(nodetaint) == 1 {
				nodeget.Spec.Taints = nodetaint[:0]
				_, err = c.client.CoreV1().Nodes().Update(context.TODO(), nodeget, metav1.UpdateOptions{})
				if err != nil {
					log.Println("node更新失败")
					return
				}
				count--
				fmt.Println("节点污点更新去除成功")
			} else if len(nodetaint) == 0 {
				return
			}
		}
	}
}

func (c *controller) enqueue(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Println(err)
		return
	}
	c.queue.Add(key)
}

func (c *controller) Run(stopCh chan struct{}) {
	go wait.Until(c.work, time.Minute, stopCh)
	<-stopCh
}

func (c *controller) work() {
	for c.process() {

	}
}

func (c *controller) process() bool {
	item, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(item)
	key := item.(string)
	if count == 1 {
		return false
	}
	ns, err := c.syncnfs(key)
	if err != nil {
		c.handleerr(key, err)
	}
	if ns != nil {
		time.Sleep(1 * time.Minute)
		c.checknode(ns)
	}
	return true
}

func (c *controller) handleerr(key string, err error) {
	if c.queue.NumRequeues(key) <= 3 {
		c.queue.AddRateLimited(key)
		return
	}
	runtime.HandleError(err)
	c.queue.Forget(err)
}

func (c *controller) podupdate(obj interface{}, obj2 interface{}) {
	c.enqueue(obj)
}

func (c *controller) podadd(obj interface{}) {
	c.enqueue(obj)
}

func Newcontroller(client *kubernetes.Clientset, dsinformer dsinformer.DaemonSetInformer, podinformer coreinformer.PodInformer) controller {
	c := controller{
		client:  client,
		dslist:  dsinformer.Lister(),
		podlist: podinformer.Lister(),
		queue:   workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
	}
	//dsinformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
	//	UpdateFunc: c.dsupdate,
	//})
	podinformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: c.podupdate,
		AddFunc:    c.podadd,
	})

	return c
}
