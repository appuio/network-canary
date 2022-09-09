# APPUiO Network Canary

[![Build](https://img.shields.io/github/workflow/status/appuio/network-canary/Test)][build]
![Go version](https://img.shields.io/github/go-mod/go-version/appuio/network-canary)
[![Version](https://img.shields.io/github/v/release/appuio/network-canary)][releases]
[![Maintainability](https://img.shields.io/codeclimate/maintainability/appuio/network-canary)][codeclimate]
[![Coverage](https://img.shields.io/codeclimate/coverage/appuio/network-canary)][codeclimate]
[![GitHub downloads](https://img.shields.io/github/downloads/appuio/network-canary/total)][releases]

[build]: https://github.com/appuio/network-canary/actions?query=workflow%3ATest
[releases]: https://github.com/appuio/network-canary/releases
[codeclimate]: https://codeclimate.com/github/appuio/network-canary

A prometheus exporter to report issues related to network connectivity.
The network-canary pings all configured targets using ICMP and tracks latency and packet loss for each of them in a histogram.


## Run

The canary simply takes a list of command line flags and will try to ping all configured endpoints.


      Usage of network-canary:
            --encoding string            How to format log output one of 'console' or 'json' (default "console")
            --metrics-addr string        Listen address for metrics server (default ":2112")
            --ping-dns strings           List of DNS names to ping to
            --ping-interval duration     How often the canary should send a ping to each target (default 1s)
            --ping-ip strings            List of IPs to ping to
            --ping-timeout duration      Timout until a ping should be considered lost (default 5s)
            --src string                 The source address
            --update-interval duration   How often the canary should fetch DNS updates (default 4s)
            --verbose                    If the canary should log debug message


### Unprivileged ICMP

This canary uses unprivileged ICMP pings. On Linux, this must be enabled with the following sysctl command:


    sudo sysctl -w net.ipv4.ping_group_range="0 2147483647"


### Kubernetes Service Discovery

The canary supports Pod IP discovery using a headless service.
To run a deployment in Kubernetes where each pod pings all other pods and exports the collected metrics, you can apply the following example config;

      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: network-canary
        labels:
          app: network-canary
      spec:
        replicas: 3
        selector:
          matchLabels:
            app: network-canary
        template:
          metadata:
            labels:
              app: network-canary
          spec:
            containers:
            - name: canary
              image: ghcr.io/appuio/network-canary:latest
              args:
                - "/canary"
                - "--ping-dns"
                - "network-canary"
              env:
                - name: POD_IP # If POD_IP is set, it will be used as 
                  valueFrom:   # the source address for reported metrics
                    fieldRef:
                      fieldPath: status.podIP
              ports:
              - containerPort: 2112
      ---
      apiVersion: v1
      kind: Service
      metadata:
        name: network-canary
      spec:
        clusterIP: None
        selector:
          app: network-canary
        ports:
          - name: metrics
            protocol: TCP
            port: 2112
            targetPort: 2112
