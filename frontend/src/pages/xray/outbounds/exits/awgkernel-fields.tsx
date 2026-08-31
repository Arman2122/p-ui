import AwgObfuscationFields from '@/pages/inbounds/form/protocols/awg-obfuscation';

import WgkernelExitFields from './wgkernel-fields';

/*
An AmneziaWG exit dials with WireGuard's own fields and adds the obfuscation the
far end already runs, so the device half is that form exactly rather than a fork
of it that would drift.

The params sit at the top level here, not under `settings`: an egress row's form
is flat, and the driver reads them back out of the row's settings JSON.
*/
export default function AwgkernelExitFields() {
  return (
    <>
      <WgkernelExitFields />
      <AwgObfuscationFields prefix="awg" />
    </>
  );
}
