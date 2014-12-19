test_remote() {
  (echo y;  sleep 3;  echo foo) |  lxc remote --config ./testconf add local 127.0.0.1:8443 --debug
  lxc remote list | grep 'local'

  lxc remote --config ./testconf set-default local
  [ "$(lxc remote --config ./testconf get-default)" = "local" ]

  lxc remote rename local foo
  lxc remote list | grep 'foo'
  lxc remote list | grep -v 'local'
  [ "$(lxc remote get-default)" = "foo" ]

  lxc remote --config ./testconf remove foo
  [ "$(lxc remote get-default)" = "" ]

  # This is a test for #91, we expect this to hang asking for a password if we
  # tried to re-add our cert.
  echo y | lxc remote add local 127.0.0.1:8443 --debug
}
