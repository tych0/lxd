#!/bin/sh

test_clustering() {

  LXD3_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD3_DIR}"
  spawn_lxd "${LXD3_DIR}"
  LXD3_ADDR=$(cat "${LXD3_DIR}/lxd.addr")
  export LXD3_ADDR

  if ! lxc_remote remote list | grep -q l1; then
    lxc_remote remote add l1 "${LXD_ADDR}" --accept-certificate --password foo
  fi
  if ! lxc_remote remote list | grep -q l2; then
    lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo
  fi
  if ! lxc_remote remote list | grep -q l3; then
    lxc_remote remote add l3 "${LXD3_ADDR}" --accept-certificate --password foo
  fi

  lxc_remote cluster enable l1 name1
  lxc_remote cluster add l1 l2 name2
  lxc_remote cluster add l2 l3 name3

  lxc_remote cluster info l1 | grep true  # make sure there's a leader

  # make sure all three nodes are in the cluster
  if [ $(lxc cluster info l1 | wc -l) -ne 9 ]; then
    lxc_remote cluster info l1
    false
  fi

  # test the raw db handling
  lxc cluster db exec l2 "create table clusterexectest(id integer primary key autoincrement not null);"
  lxc cluster db dump l2 > "${TEST_DIR}"/dump.sql
  sqlite3 "${TEST_DIR}"/dump.sql .schema | grep clusterexectest

  lxc_remote cluster remove l3 name2

  # make sure leave worked
  bad=0
  lxc_remote cluster info l1 | grep name2 || bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "name2 didn't leave the cluster"
    false
  fi

  kill_lxd "${LXD3_DIR}"
}
