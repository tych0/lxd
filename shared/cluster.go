package shared

type ClusterMember struct {
	Leader      bool   `json:"leader"`
	Addr        string `json:"addr"`
	Name        string `json:"name"`
	Certificate string `json:"certificate"`
}

type ClusterStatus struct {
	Members []ClusterMember `json:"members"`
}
