package meta

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/redis/go-redis/v9"
	"k8s.io/klog"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

func NewClient(uri string) (redis.UniversalClient, error) {
	var err error
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}
	p := strings.Index(uri, "://")
	if p < 0 {
		klog.V(5).Infof("invalid uri: %s", uri)
	}
	driver := uri[:p]

	m, err := newRedisMeta(driver, uri[p+3:])
	if err != nil {
		klog.V(5).Infof("Meta %s is not available: %s", uri, err)
	}
	return m, err
}

func newRedisMeta(driver, addr string) (redis.UniversalClient, error) {
	uri := driver + "://" + addr
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("url parse %s: %s", uri, err)
	}
	values := u.Query()
	query := queryMap{&values}
	minRetryBackoff := query.duration("min-retry-backoff", "min_retry_backoff", time.Millisecond*20)
	maxRetryBackoff := query.duration("max-retry-backoff", "max_retry_backoff", time.Second*10)
	readTimeout := query.duration("read-timeout", "read_timeout", time.Second*30)
	writeTimeout := query.duration("write-timeout", "write_timeout", time.Second*5)
	skipVerify := query.pop("insecure-skip-verify")
	routeRead := query.pop("route-read")
	certFile := query.pop("tls-cert-file")
	keyFile := query.pop("tls-key-file")
	caCertFile := query.pop("tls-ca-cert-file")
	u.RawQuery = values.Encode()

	hosts := u.Host
	opt, err := redis.ParseURL(u.String())
	if err != nil {
		return nil, fmt.Errorf("redis parse %s: %s", uri, err)
	}
	if opt.TLSConfig != nil {
		opt.TLSConfig.ServerName = "" // use the host of each connection as ServerName
		opt.TLSConfig.InsecureSkipVerify = skipVerify != ""
		if certFile != "" {
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, fmt.Errorf("get certificate error certFile:%s keyFile:%s error:%s", certFile, keyFile, err)
			}
			opt.TLSConfig.Certificates = []tls.Certificate{cert}
		}
		if caCertFile != "" {
			caCert, err := os.ReadFile(caCertFile)
			if err != nil {
				return nil, fmt.Errorf("read ca cert file error path:%s error:%s", caCertFile, err)
			}
			caCertPool := x509.NewCertPool()
			caCertPool.AppendCertsFromPEM(caCert)
			opt.TLSConfig.RootCAs = caCertPool
		}
	}
	if opt.Password == "" {
		opt.Password = os.Getenv("REDIS_PASSWORD")
	}
	if opt.Password == "" {
		opt.Password = os.Getenv("META_PASSWORD")
	}
	opt.MaxRetries = -1 // Redis use -1 to disable retries
	opt.MinRetryBackoff = minRetryBackoff
	opt.MaxRetryBackoff = maxRetryBackoff
	opt.ReadTimeout = readTimeout
	opt.WriteTimeout = writeTimeout
	var rdb redis.UniversalClient
	if strings.Contains(hosts, ",") && strings.Index(hosts, ",") < strings.Index(hosts, ":") {
		var fopt redis.FailoverOptions
		ps := strings.Split(hosts, ",")
		fopt.MasterName = ps[0]
		fopt.SentinelAddrs = ps[1:]
		_, port, _ := net.SplitHostPort(fopt.SentinelAddrs[len(fopt.SentinelAddrs)-1])
		if port == "" {
			port = "26379"
		}
		for i, addr := range fopt.SentinelAddrs {
			h, p, e := net.SplitHostPort(addr)
			if e != nil {
				fopt.SentinelAddrs[i] = net.JoinHostPort(addr, port)
			} else if p == "" {
				fopt.SentinelAddrs[i] = net.JoinHostPort(h, port)
			}
		}
		fopt.SentinelPassword = os.Getenv("SENTINEL_PASSWORD")
		fopt.DB = opt.DB
		fopt.Username = opt.Username
		fopt.Password = opt.Password
		fopt.TLSConfig = opt.TLSConfig
		fopt.MaxRetries = opt.MaxRetries
		fopt.MinRetryBackoff = opt.MinRetryBackoff
		fopt.MaxRetryBackoff = opt.MaxRetryBackoff
		fopt.ReadTimeout = opt.ReadTimeout
		fopt.WriteTimeout = opt.WriteTimeout
		fopt.PoolSize = opt.PoolSize               // default: GOMAXPROCS * 10
		fopt.PoolTimeout = opt.PoolTimeout         // default: ReadTimeout + 1 second.
		fopt.MinIdleConns = opt.MinIdleConns       // disable by default
		fopt.MaxIdleConns = opt.MaxIdleConns       // disable by default
		fopt.ConnMaxIdleTime = opt.ConnMaxIdleTime // default: 30 minutes
		fopt.ConnMaxLifetime = opt.ConnMaxLifetime // disable by default
		rdb = redis.NewFailoverClient(&fopt)
	} else {
		if !strings.Contains(hosts, ",") {
			c := redis.NewClient(opt)
			info, err := c.ClusterInfo(context.Background()).Result()
			if err != nil && strings.Contains(err.Error(), "cluster mode") || err == nil && strings.Contains(info, "cluster_state:") {
				klog.Errorf("redis %s is in cluster mode", hosts)
			} else {
				rdb = c
			}
		}
		if rdb == nil {
			var copt redis.ClusterOptions
			copt.Addrs = strings.Split(hosts, ",")
			copt.MaxRedirects = 1
			copt.Username = opt.Username
			copt.Password = opt.Password
			copt.TLSConfig = opt.TLSConfig
			copt.MaxRetries = opt.MaxRetries
			copt.MinRetryBackoff = opt.MinRetryBackoff
			copt.MaxRetryBackoff = opt.MaxRetryBackoff
			copt.ReadTimeout = opt.ReadTimeout
			copt.WriteTimeout = opt.WriteTimeout
			copt.PoolSize = opt.PoolSize               // default: GOMAXPROCS * 10
			copt.PoolTimeout = opt.PoolTimeout         // default: ReadTimeout + 1 second.
			copt.MinIdleConns = opt.MinIdleConns       // disable by default
			copt.MaxIdleConns = opt.MaxIdleConns       // disable by default
			copt.ConnMaxIdleTime = opt.ConnMaxIdleTime // default: 30 minutes
			copt.ConnMaxLifetime = opt.ConnMaxLifetime // disable by default
			switch routeRead {
			case "random":
				copt.RouteRandomly = true
			case "latency":
				copt.RouteByLatency = true
			case "replica":
				copt.ReadOnly = true
			default:
				// route to primary
			}
			rdb = redis.NewClusterClient(&copt)
		}
	}
	return rdb, nil
}
