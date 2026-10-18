package proxy

import "strings"

// 容器裡看得到的模型設定。只有代理網址和 worker-key，真金鑰永遠不在這裡（SEC-5）。
const (
	EnvModelBaseURL = "XBOT_MODEL_BASE_URL"
	EnvWorkerKey    = "XBOT_WORKER_KEY"
)

// ContainerEnv 回傳建立 Bot 容器時要放進去的模型環境變數。
// proxyBase 是容器連得到的服務位址（例：http://host.docker.internal:8780），由建容器的任務卡決定。
func ContainerEnv(proxyBase, providerID string, workerKey Secret) map[string]string {
	return map[string]string{
		EnvModelBaseURL: strings.TrimRight(proxyBase, "/") + Prefix + providerID + "/v1",
		EnvWorkerKey:    string(workerKey),
	}
}
