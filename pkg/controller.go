package pkg

import (
	"context"
	"fmt"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	"reflect"
	"strconv"
	"time"
)

var count int

type controller struct {
	client  *kubernetes.Clientset
	dslist  appslist.DaemonSetLister
	podlist corelist.PodLister
	nslist  corelist.NamespaceLister
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

// 是否大于一半节点数
func (c *controller) nodecount() string {
	nodelist, err := c.client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Println(err)
		return "节点清单未拿到"
	}
	if count > len(nodelist.Items)/2 {
		log.Println("nfs宕机节点过半 需要进行手动切换")
		return "false"
	}
	return "null"
}

// 回收资源
func (c *controller) collection() {
	ns, err := c.nslist.Get("nfs-watch")
	if err != nil && errors.IsNotFound(err) {
		return
	}
	for i, _ := range ns.Labels {
		if i == "delete" {
			c.client.AppsV1().DaemonSets("nfs-watch").Delete(context.TODO(), "nfs-watch-ds", metav1.DeleteOptions{})
			c.client.CoreV1().PersistentVolumeClaims("nfs-watch").Delete(context.TODO(), "nfs-watch-pvc", metav1.DeleteOptions{})
			c.client.CoreV1().PersistentVolumes().Delete(context.TODO(), "nfs-watch-pv", metav1.DeleteOptions{})
			c.client.StorageV1().StorageClasses().Delete(context.TODO(), "nfs-pro-class", metav1.DeleteOptions{})
			delete(ns.Labels, "delete")
			c.client.CoreV1().Namespaces().Update(context.TODO(), ns, metav1.UpdateOptions{})
		} else if i == "create" {
			err := c.nfscreate()
			if err != nil && !errors.IsNotFound(err) {
				log.Println(err)
			}
			delete(ns.Labels, "create")
			c.client.CoreV1().Namespaces().Update(context.TODO(), ns, metav1.UpdateOptions{})
		}
	}
}

//func (c *controller) dsupdate(oldobj interface{}, newobj interface{}) {
//	oldds := oldobj.(*apps.DaemonSet)
//	newds := newobj.(*apps.DaemonSet)
//	if oldds.Status.NumberReady != newds.Status.NumberReady {
//		c.enqueue(newobj)
//	}
//	return
//}

func (c *controller) nfscreate() error {
	_, err := c.client.CoreV1().PersistentVolumes().Get(context.TODO(), "nfs-watch-pv", metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		pv := c.pvcreate()
		_, err = c.client.CoreV1().PersistentVolumes().Create(context.TODO(), pv, metav1.CreateOptions{})
		if err != nil {
			log.Println(err)
		}
	}
	_, err = c.client.CoreV1().PersistentVolumeClaims("nfs-watch").Get(context.TODO(), "nfs-watch-pvc", metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		pvc := c.pvccreate()
		_, err = c.client.CoreV1().PersistentVolumeClaims("nfs-watch").Create(context.TODO(), pvc, metav1.CreateOptions{})
		if err != nil {
			log.Println(err)
		}
	}
	_, err = c.client.AppsV1().DaemonSets("nfs-watch").Get(context.TODO(), "nfs-watch-ds", metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		nfsds := c.nfsdscreate()
		_, err = c.client.AppsV1().DaemonSets("nfs-watch").Create(context.TODO(), nfsds, metav1.CreateOptions{})
		if err != nil {
			log.Println(err)
		}
	}
	_, err = c.client.StorageV1().StorageClasses().Get(context.TODO(), "nfs-pro-class", metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		sc := c.sccreate()
		_, err = c.client.StorageV1().StorageClasses().Create(context.TODO(), sc, metav1.CreateOptions{})
		if err != nil {
			log.Println(err)
		}
	}

	return nil
}

func (c *controller) namespace() *core.Namespace {
	ns := core.Namespace{}
	ns.Name = "nfs-watch"
	return &ns
}

func (c *controller) sccreate() *storagev1.StorageClass {
	mode := storagev1.VolumeBindingImmediate
	policy := core.PersistentVolumeReclaimPolicy("Retain")
	b := true
	sc := storagev1.StorageClass{}
	sc.Name = "nfs-pro-class"
	sc.Provisioner = "nfs-controller"
	sc.VolumeBindingMode = &mode
	sc.ReclaimPolicy = &policy
	sc.AllowVolumeExpansion = &b
	return &sc
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
				Server: "10.182.0.34",
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
											Key:      "node-role.kubernetes.io/control-plane",
											Operator: "DoesNotExist",
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
						Image: "nfscheck:v1.1",
						Command: []string{
							"sh",
							"-c",
							"while true;do for i in {1..10000000};do echo $i > /data/nfs-test/test.txt && sleep 1;if [ $? -eq 1 ];then exit 1;fi;done;done",
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

func (c *controller) syncnfs(key string) ([]string, *core.Pod, error) {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		log.Println(err)
		return nil, nil, err
	}
	if namespace != "nfs-watch" {
		return nil, nil, err
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

	podget, err := c.podlist.Pods(namespace).Get(name)
	if errors.IsNotFound(err) {
		log.Println(err)
		return nil, nil, err
	}
	_, ok := podget.GetAnnotations()["nfs-node-watch"]

	phase := podget.Status.Phase
	if ok {
		if phase == "Running" {
			containerready := podget.Status.ContainerStatuses[0].Ready
			if !containerready {
				nodes, err := c.client.CoreV1().Nodes().Get(context.TODO(), podget.Spec.NodeName, metav1.GetOptions{})
				if err != nil {
					log.Println(err)
					return nil, nil, err
				}
				taint := &core.Taint{
					Key:    "nfs-client-mount-error",   // 污点的键，可以根据需要进行自定义设置
					Value:  "true",                     // 污点的值，可以根据需要进行自定义设置
					Effect: core.TaintEffectNoSchedule, // 设置污点效果为NoSchedule，可以根据需要进行自定义设置其他效果，如Evict（驱逐）等。
				}
				//检测节点是否有驱逐污点,节点没污点直接打污点，有污点的话进行判断
				nodetaint := nodes.Spec.Taints

				if nodetaint == nil {
					nodes.Spec.Taints = append(nodetaint, *taint)
					_, err = c.client.CoreV1().Nodes().Update(context.TODO(), nodes, metav1.UpdateOptions{})
					if err != nil {
						log.Println(err)
						return nil, nil, err
					}
					fmt.Printf("%v 污点已添加\n", nodes.Name)
					count++
					return nil, podget, nil

				} else if nodetaint != nil {
					for _, o := range nodetaint {
						if o.Key == "nfs-client-mount-error" {
							return nil, nil, nil
						}
					}
					nodes.Spec.Taints = append(nodetaint, *taint)
					_, err = c.client.CoreV1().Nodes().Update(context.TODO(), nodes, metav1.UpdateOptions{})
					if err != nil {
						log.Println(err)
						return nil, nil, err
					}
				}
				fmt.Printf("%v 污点已添加\n", nodes.Name)
				count++
				return nil, podget, nil
			} else if containerready && podget.Status.ContainerStatuses[0].RestartCount > 0 {
				return ns, nil, err
			} else if containerready && podget.Status.ContainerStatuses[0].RestartCount == 0 {
				fmt.Printf("%v %v pod已重建或首次读取pod信息\n", namespace, name)
				return ns, nil, err
			}
		}
	}

	return nil, nil, nil
}
func (c *controller) syncbspod(nfspod *core.Pod) {
	_, err := c.podlist.Pods(nfspod.Namespace).Get(nfspod.Name)
	if err != nil && errors.IsNotFound(err) {
		log.Println(err)
		return
	}
	phase := nfspod.Status.Phase
	containerready := nfspod.Status.ContainerStatuses[0].Ready
	if phase == "Running" && !containerready {
		allns, err := c.nslist.List(labels.Everything())
		if err != nil {
			log.Println(err)
			return
		}
		for _, ns := range allns {
			allpods, err := c.podlist.Pods(ns.Name).List(labels.Everything())
			if err != nil {
				log.Println(err)
				return
			}
			for _, pod := range allpods {
				_, ok := pod.GetAnnotations()["nfs-check"]
				if ok {
					if nfspod.Spec.NodeName == pod.Spec.NodeName {
						err := c.client.CoreV1().Pods(ns.Name).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
						if err != nil && errors.IsNotFound(err) {
							log.Println(err)
							return
						}
						fmt.Printf("%v %v pod已删除\n", ns.Name, pod.Name)
					}
				}
			}
		}
	}
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
					log.Println("node 更新失败")
					return
				}
				fmt.Printf("节点 %v 污点更新去除成功\n", nodeget.Name)
				count--

			} else if o.Key == "nfs-client-mount-error" && len(nodetaint) == 1 {
				nodeget.Spec.Taints = nodetaint[:0]
				_, err = c.client.CoreV1().Nodes().Update(context.TODO(), nodeget, metav1.UpdateOptions{})
				if err != nil {
					log.Println("node更新失败")
					return
				}

				fmt.Printf("节点 %v 污点更新去除成功\n", nodeget.Name)
				count--
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
	c.collection()
	item, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(item)
	key := item.(string)
	ns, nfspod, err := c.syncnfs(key)

	if nfspod != nil {
		time.Sleep(20 * time.Second)
		c.syncbspod(nfspod)
	}
	if err != nil {
		c.handleerr(key, err)
	}
	if ns != nil {
		time.Sleep(20 * time.Second)
		c.checknode(ns)
	}
	string := c.nodecount()
	if string == "false" {
		return false
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
	if reflect.DeepEqual(obj, obj2) {
		return
	}
	c.enqueue(obj2)
}

func (c *controller) podadd(obj interface{}) {
	c.enqueue(obj)
}

func (c *controller) nsupdate(obj interface{}, obj2 interface{}) {
	if reflect.DeepEqual(obj, obj2) {
		return
	}
	c.enqueue(obj2)
}

func (c *controller) nsadd(obj interface{}) {
	c.enqueue(obj)
}

func Newcontroller(client *kubernetes.Clientset, dsinformer dsinformer.DaemonSetInformer, podinformer coreinformer.PodInformer, nsinformer coreinformer.NamespaceInformer) controller {
	c := controller{
		client:  client,
		dslist:  dsinformer.Lister(),
		podlist: podinformer.Lister(),
		nslist:  nsinformer.Lister(),
		queue:   workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
	}
	//dsinformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
	//	UpdateFunc: c.dsupdate,
	//})
	podinformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: c.podupdate,
		AddFunc:    c.podadd,
	})

	nsinformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: c.nsupdate,
		AddFunc:    c.nsadd,
	})

	return c
}
