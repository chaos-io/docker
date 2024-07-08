# docker-compose.yaml 格式



### volumes 
```
version: '3'
services:
  myapp:
    image: your_image:tag
    volumes:
      - mydata:/app/data

volumes:
  mydata:
    driver: local
    driver_opts:
      type: none
      o: bind
      device: /path/on/host
```
上述demo使用的是本地驱动 (local)，并通过 type: none、o: bind 和 device: /path/on/host 指定了绑定挂载的方式。
type: none 表示不使用特定的卷类型。
o: bind 表示使用绑定挂载。
device: /path/on/host 指定了宿主机上卷的路径。
