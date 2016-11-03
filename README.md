# Prometheus simple alerter

[alertmanager](https://github.com/prometheus/alertmanager) is a very powerful tool to send prometheus alert to different targets. But it is also very complicated. Many teams only need to send alert message to Slack, so this simple alerter.

```
go get github.com/mediocregopher/radix.v2/redis
GOOS=linux GOARCH=amd64 go build -o molert
# start redis server before running molert
./molert -expiration=180 -frequency=60 -silence_duration=3600 -redis_url="127.0.0.1:6379" -slack_webhook="https://hooks.slack.com/services/xxxxxx" -listen_addr="0.0.0.0:90093" -external_url="http://www.example.com:9093"
```

* `expiration`: Expiration time in seconds, if no more alert message fired in this time, this alert will disappear. Default 180 aka 3min
* `frequency`: Alert frequency in seconds. Default 60 aka 1min
* `silence_duration`: Silence duration in seconds, if problem not fixed during this time, alert will fire again. Default 3600 aka 1hour
* `redis_url`: Redis server url, redis is used to store alert status. Default "127.0.0.1:6379"
* `external_url`: URL under which molert is externally reachable, alert can be silenced by this URL with curl, the command is sent with alert msg to slack
* `listen_addr`: Molert http server listen on this address, set `alertmanager.url` to this url addr. Default "0.0.0.0:9093"
* `slack_webhook`: slack webhook url
