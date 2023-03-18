#!/bin/bash

set -euo pipefail

workdir=$(mktemp -d)

dd if=/dev/urandom bs=1M count=100 of=${workdir}/input.img status=none
input_sha=$(sha256sum ${workdir}/input.img | awk '{print $1}')

bin/rpiped -timeout 1s &
rpiped_pid=$!
cat ${workdir}/input.img | bin/rpipe -job ${workdir} -url http://127.0.0.1:8000 -command "tee ${workdir}/output.img" &
rpipe_pid=$!

wait ${rpipe_pid}
kill ${rpiped_pid}
wait

output_sha=$(sha256sum ${workdir}/output.img | awk '{print $1}')

# rm -r ${workdir}

if [ "$input_sha" != "$output_sha" ];then
    echo "sha256 mismatch"
    echo "input:  $input_sha"
    echo "output: $output_sha"
    exit 1
fi



