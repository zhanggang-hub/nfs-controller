package backup

import (
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// 字符串转换为资源单位
//func resourceQuantityFromStr(capacity string) resource.Quantity {
//	// 将字符串转换为字节数 10进制转换为64位整数
//	bytes, err := strconv.ParseInt(capacity, 10, 64)
//	if err != nil {
//		fmt.Println("无法将容量字符串转换为字节数:", err)
//		return resource.MustParse("0")
//	}
//	// 转换为合适的资源单位（例如，1Gi 转换为 1 * resource.Gibibyte）
//	quantity := resource.NewQuantity(bytes, resource.BinarySI)
//	return *quantity
//}

// 查看需要backup的pvc
func Backuppvc(pv *core.PersistentVolume, pvc *core.PersistentVolumeClaim) {
	pvcreate(pv)
	pvccreate(pvc)
}

func pvcreate(pv2 *core.PersistentVolume) *core.PersistentVolume {
	pv := core.PersistentVolume{}
	pv.Name = pv2.Name + "-" + "backup"
	pv.Spec = core.PersistentVolumeSpec{
		Capacity: map[core.ResourceName]resource.Quantity{
			core.ResourceStorage: pv2.Spec.Capacity[core.ResourceStorage],
		},
		PersistentVolumeReclaimPolicy: pv2.Spec.PersistentVolumeReclaimPolicy,
		StorageClassName:              "nfs-pro-class",
		AccessModes:                   pv2.Spec.AccessModes,
		VolumeMode:                    pv2.Spec.VolumeMode,
		PersistentVolumeSource: core.PersistentVolumeSource{
			NFS: &core.NFSVolumeSource{
				Path:   pv2.Spec.NFS.Path,
				Server: "192.168.40.130",
			},
		},
	}
	return &pv
}

func pvccreate(pvc2 *core.PersistentVolumeClaim) *core.PersistentVolumeClaim {
	sc := "nfs-pro-class"
	pvc := core.PersistentVolumeClaim{}
	pvc.Name = pvc2.Name + "-" + "backup"
	pvc.Namespace = pvc2.Namespace
	pvc.Spec = core.PersistentVolumeClaimSpec{
		StorageClassName: &sc,
		AccessModes:      pvc2.Spec.AccessModes,
		Resources: core.VolumeResourceRequirements{
			Requests: map[core.ResourceName]resource.Quantity{
				core.ResourceStorage: pvc2.Spec.Resources.Requests[core.ResourceStorage],
			},
		},
	}
	return &pvc
}
