package shared

type ClusterMember struct {
	Leader bool   `json:"leader"`
	Addr   string `json:"addr"`
}

type ClusterStatus struct {
	Members []ClusterMember `json:"members"`
}
