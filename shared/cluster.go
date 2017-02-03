package shared

type ClusterMember struct {
	Leader      bool   `json:"leader"`
	Addr        string `json:"addr"`
	Name        string `json:"name"`
}

type ClusterStatus struct {
	Members []ClusterMember `json:"members"`
}
