1.把镜像上传后，apply yaml文件，会生成一个nfs-watch命名空间。


       ※对nfs-watch命名空间进行 label操作 label为create=true 会自动创建相关监控nfs资源，delete=true 会删除相关监控nfs资源。


2.对需要监控的pod打一个annotations,为annotation: nfs-chexk: "true"。


       ※当此节点的nfs连接失败，daemonset的检测pod失败后，controller会监听到pod事件变化，给此节点打禁止调度污点。20s后判断再次判断检测pod，失败后删除被监控pod完成驱逐。
       

3.当集群的nfs宕机节点超过一半数量后，controller会夯死，需要手动回收污点以及pod。


       ※使用时需要更新nfs ip以及path。
