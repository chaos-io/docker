#!/bin/bash

env GOOS=linux GOARCH=amd64 go build -o start
scp start root@143:/root/port
