package main

import (
	"bytes"
	"context"
	"flag"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	V1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/fsnotify/fsnotify"
)

var (
	configFile    string
	loglevel      string
	metricsListen string
	kubeconfig    *string
	config        *rest.Config
	clientset     *kubernetes.Clientset

	hostname = os.Getenv("HOSTNAME")
	rootfs   string
	PVS      []V1.PersistentVolume

	watcher *fsnotify.Watcher
	err     error

	//registeredVolumes []prometheus.GaugeFunc
	//registeredVolumes = make(map[string]prometheus.GaugeFunc)

	registeredMetrics = make(map[string][]prometheus.GaugeFunc)

	metricUploadedSuccess = make(map[string]float64)
	metricUploadedError = make(map[string]float64)

	alreadyAddedFiles []string
)

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func removeStringFromSlice(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func getS3Client(config *cfg) (*minio.Client, error) {
	log.Trace("getS3Client")
	minioClient, err := minio.New(config.S3.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.S3.AccessKeyID, config.S3.SecretAccessKey, ""),
		Secure: config.S3.UseSSL,
	})
	if err != nil {
		return nil, err
	}
	return minioClient, nil
}

func walkHandler(path string, file os.FileInfo, err error) error {
	l := log.WithFields(log.Fields{
		"hostname": hostname,
		"function": "walkHandler",
		"path":     path,
	})

	l.Trace("walkHandler")
	if err != nil {
		l.Error(err)
		return err
	}
	if file.IsDir() {
		return nil
	}
	l.Trace("processing")

	if stringInSlice(path, alreadyAddedFiles) {
		l.Debug("already watching file")
		return nil
	}

	for _, el := range configuration.Notify.Match.Names {
		if strings.HasSuffix(path, el) {
			l.Info("added")
			err = watcher.Add(path)
			if err != nil {
				log.Error(err)
				return err
			}
			alreadyAddedFiles = append(alreadyAddedFiles, path)

			_, err = uploadFile(l, path)
			if err != nil {
				log.Error(err)
			}

			registeredMetrics[path] = generateMetrics(path, prometheus.Labels{
				"hostname": hostname,
				"path": "path",
			})
		}
	}
	return nil
}

func generateMetrics(path string, labels prometheus.Labels) []prometheus.GaugeFunc {
	var (
		result []prometheus.GaugeFunc
	)

	labels["path"] = path

	result = append(
		result,
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Namespace:   configuration.Metrics.Namespace,
				Name:        "upload_success",
				ConstLabels: labels,
			},
			func() float64 {
				return metricUploadedSuccess[path]
			},
		),
	)

	result = append(
		result,
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Namespace:   configuration.Metrics.Namespace,
				Name:        "upload_error",
				ConstLabels: labels,
			},
			func() float64 {
				return metricUploadedError[path]
			},
		),
	)

	result = append(
		result,
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Namespace:   configuration.Metrics.Namespace,
				Name:        "size",
				ConstLabels: labels,
			},
			func() float64 {
				fi, err := os.Stat(path)
				if err != nil {
					return float64(-1)
				}
				return float64(fi.Size())
			},
		),
	)

	result = append(
		result,
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Namespace:   configuration.Metrics.Namespace,
				Name:        "mod_time",
				ConstLabels: labels,
			},
			func() float64 {
				fi, err := os.Stat(path)
				if err != nil {
					return float64(-1)
				}
				return float64(fi.ModTime().Unix())
			},
		),
	)

	for _, m := range result {
		if err := prometheus.Register(m); err != nil {
			log.Error(err)
		}
	}
	return result
}

func uploadFile(l *log.Entry, path string) (minio.UploadInfo, error) {
	S3Client, err := getS3Client(configuration)
	if err != nil {
		l.Error(err)
		return minio.UploadInfo{}, err
	}
	l.Debug("reading file contents")
	dat, err := ioutil.ReadFile(path)
	if err != nil {
		l.Error(err)
		return minio.UploadInfo{}, err
	}
	l.Debug("uploading file to s3")
	r := bytes.NewReader(dat)

	info, err := S3Client.PutObject(context.TODO(), configuration.S3.Bucket, strings.Replace(path, "/", "", 1), r, int64(len(dat)), minio.PutObjectOptions{
		ContentType: configuration.S3.ContentType,
		StorageClass: configuration.S3.StorageClass,
	})

	if err != nil {
		l.Error(err)
		metricUploadedError[path] += float64(1)
		return minio.UploadInfo{}, err
	}

	metricUploadedSuccess[path] += float64(1)

	return info, nil
}

func main() {
	initApp()

	go func() {
		log.WithFields(log.Fields{
			"hostname": hostname,
			"function": "metricsServer",
			"metricsListen": metricsListen,
		}).Info("Starting webserver")

		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(metricsListen, nil)
	}()

	//log.Info("fsnotify.NewWatcher()")
	watcher, err = fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	go func() {
		//for _, el := range configuration.Notify.Files {
		//	log.Info("watcher.Add ", el)
		//	err = watcher.Add(el)
		//	if err != nil {
		//		log.Error(err)
		//	}
		//}

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				l := log.WithFields(log.Fields{
					"hostname": hostname,
					"function":  "notifyLoop",
					"name":      event.Name,
					"operation": event.Op,
				})

				if event.Op == fsnotify.Remove {
					l.Info("removing from known files")
					watcher.Remove(event.Name)
					alreadyAddedFiles = removeStringFromSlice(alreadyAddedFiles, event.Name)
					for _, m := range registeredMetrics[event.Name] {
						if !prometheus.Unregister(m) {
							log.Error(err)
						}
					}
					registeredMetrics[event.Name] = []prometheus.GaugeFunc{}
				}
				if event.Op == fsnotify.Write {
					l.Info("modified file")

					info, err := uploadFile(l, event.Name)
					if err != nil {
						l.Error(err)
					}

					log.WithFields(log.Fields{
						"hostname": hostname,
						"function":  "notifyLoop",
						"name":      event.Name,
						"operation": event.Op,
						"info":      info,
					}).Info("uploaded file")
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					log.Error("error:", err)
					return
				}
			}
		}
	}()

	for {
		time.Sleep(time.Second)
		PVS = gather()

		for _, pv := range PVS {
			log.WithFields(log.Fields{
				"pv": pv.ObjectMeta.Name,
			}).Trace("Already registered")

			pvPath := rootfs + pv.Spec.HostPath.Path
			filepath.Walk(pvPath, walkHandler)
		}
	}
}

func initApp() {
	inCluster := flag.Bool("in-cluster", false, "is in cluster start")
	flag.StringVar(&hostname, "hostname", hostname, "host to search pv")
	flag.StringVar(&rootfs, "rootfs", "", "prefix for datadir")
	flag.StringVar(&loglevel, "loglevel", "info", "[trace, debug, info]")
	flag.StringVar(&metricsListen, "listen-addr", "0.0.0.0:2112", "bind http server")
	flag.StringVar(&configFile, "config", "config.yaml", "path to config file")

	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	flag.Parse()

	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)

	switch loglevel {
	case "trace":
		log.SetLevel(log.TraceLevel)
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	default:
		log.SetLevel(log.InfoLevel)
	}

	switch *inCluster {
	case true:
		err = inClusterClient()
	case false:
		err = outOfClusterClient()
	}

	if err != nil {
		log.Error(err)
	}

	_, err = loadConfig(configFile)
	if err != nil {
		log.Fatal(err)
	}
}

func gather() []V1.PersistentVolume {
	log.Debug("gather()")
	var (
		result []V1.PersistentVolume
	)

	pvs, err := clientset.CoreV1().PersistentVolumes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Error(err)
	}

	for _, pv := range pvs.Items {
		log.Tracef("gather: processing: %v", pv.ObjectMeta.Name)
		// not exist path to dir
		if pv.Spec.HostPath == nil {
			log.Tracef("gather: %v: pv.Spec.HostPath == nil", pv.ObjectMeta.Name)
			continue
		}
		// empty node selector
		if pv.Spec.NodeAffinity == nil {
			log.Tracef("gather: %v: pv.Spec.NodeAffinity == nil", pv.ObjectMeta.Name)
			continue
		}
		if len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms) == 0 {
			log.Tracef("gather: %v: len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms) == 0", pv.ObjectMeta.Name)
			continue
		}
		if len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions) == 0 {
			log.Tracef("gather: %v: len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions) == 0", pv.ObjectMeta.Name)
			continue
		}
		if pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Key != "kubernetes.io/hostname" {
			log.Tracef("gather: %v: pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Key != \"kubernetes.io/hostname\"", pv.ObjectMeta.Name)
			continue
		}
		if len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values) == 0 {
			log.Tracef("gather: %v: len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values) == 0", pv.ObjectMeta.Name)
			continue
		}
		// not my hostname
		if pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values[0] != hostname {
			log.Tracef("gather: %v: pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values[0] != %v", pv.ObjectMeta.Name, hostname)
			continue
		}
		log.Tracef("gather: append: %v", pv.ObjectMeta.Name)
		result = append(result, pv)
	}
	return result
}

func homeDir() string {
	log.Debug("homeDir()")
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}

func outOfClusterClient() error {
	log.Debug("outOfClusterClient()")
	config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		return err
	}
	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	return nil
}

func inClusterClient() error {
	log.Debug("inClusterClient()")
	config, err = rest.InClusterConfig()
	if err != nil {
		return err
	}
	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	return nil
}
