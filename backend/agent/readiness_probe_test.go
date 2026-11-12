package agent

import (
	"os/exec"
	"strings"
	"testing"
)

func TestReadinessProbeNativeHealthAndGeneration(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	command := exec.Command(python, "-I", "-c", readinessProbeTestDriver)
	command.Stdin = strings.NewReader(connectionReadinessProbe)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("readiness protocol: %v\n%s", err, output)
	}
}

// Exercise the actual production probe in a private temporary cache layout.
const readinessProbeTestDriver = `import contextlib,hashlib,io,json,os,tempfile,threading,time,unittest
from pathlib import Path
from unittest.mock import patch
probe=__import__('sys').stdin.read()
class Protocol(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
  self.root=Path(self.tmp.name)
  self.identity={'agentId':'agent','projectId':'project','workspace':'/workspace','temporaryDirectory':'/tmp/zentrale-agent'}
  cache=self.root/('zentrale-microvm-'+str(os.getuid()))
  self.scope=cache/'agents'/hashlib.sha256(b'project\0agent').hexdigest()
  self.control=self.scope/'session'
  for directory in (cache,cache/'agents',self.scope,self.control): directory.mkdir(mode=0o700)
  self.write(self.scope,'identity.json',self.identity)
  (self.scope/'lock').touch()
  self.write(self.control,'ready.json',{'bootId':'11111111-2222-3333-4444-555555555555'})
  self.write(self.control,'request.json',{'storage':[]})
  (self.control/'version').write_text('native-runner-version')
  (self.control/'supervisor.pid').write_text(str(os.getpid()))
  (self.control/'heartbeat').touch()
  self.stop=threading.Event()
  self.reboot_on_health=False
  def respond():
   while not self.stop.wait(0.005):
    try:
     request=json.loads((self.control/'health.request').read_text())
     if self.reboot_on_health: self.write(self.control,'ready.json',{'bootId':'bbbbbbbb-2222-3333-4444-555555555555'})
     self.write(self.control,'health.response',request)
    except (FileNotFoundError,ValueError): pass
  self.thread=threading.Thread(target=respond); self.thread.start()
  self.addCleanup(lambda:(self.stop.set(),self.thread.join()))
 def write(self,directory,name,value):
  temporary=directory/(name+'.test')
  temporary.write_text(json.dumps(value)); os.replace(temporary,directory/name)
 def run_probe(self,identity=None,timeout=False):
  script=probe.replace("Path('/var/tmp')",'Path('+repr(str(self.root))+')')
  if timeout: script=script.replace('time.monotonic()+5','time.monotonic()+0.1')
  output=io.StringIO()
  with patch('sys.stdin',io.StringIO(json.dumps(identity or self.identity))),contextlib.redirect_stdout(output): exec(script,{})
  return output.getvalue().strip()
 def test_nonce_and_generation(self):
  first=self.run_probe(); self.assertEqual(len(first),64)
  self.assertEqual(first,self.run_probe())
  self.write(self.control,'ready.json',{'bootId':'aaaaaaaa-2222-3333-4444-555555555555'})
  second=self.run_probe(); self.assertNotEqual(first,second)
  (self.control/'version').write_text('changed-runner-version')
  self.assertNotEqual(second,self.run_probe())
 def test_stop_and_disconnect(self):
  (self.control/'stopped').touch()
  with self.assertRaises(AssertionError): self.run_probe()
  (self.control/'stopped').unlink()
  (self.control/'supervisor.pid').write_text('2147483647')
  with self.assertRaises(ProcessLookupError): self.run_probe()
 def test_reboot_during_health_probe(self):
  self.reboot_on_health=True
  with self.assertRaises(AssertionError): self.run_probe()
 def test_prepared_boot_generation_is_required(self):
  with patch('sys.argv',['probe','aaaaaaaa-2222-3333-4444-555555555555']):
   with self.assertRaises(AssertionError): self.run_probe()
  self.assertEqual(len(self.run_probe()),64)
 def test_scope_and_symlink(self):
  changed=dict(self.identity,workspace='/other-project')
  with self.assertRaises(AssertionError): self.run_probe(changed)
  (self.control/'version').unlink(); (self.control/'version').symlink_to('/etc/passwd')
  with self.assertRaises(OSError): self.run_probe()
 def test_unresponsive_guest_not_just_live_supervisor(self):
  self.stop.set(); self.thread.join()
  with self.assertRaises(RuntimeError): self.run_probe(timeout=True)
 def test_missing_and_nonprivate_control(self):
  (self.control/'ready.json').unlink()
  with self.assertRaises(FileNotFoundError): self.run_probe()
  self.write(self.control,'ready.json',{'bootId':'11111111-2222-3333-4444-555555555555'})
  self.control.chmod(0o755)
  with self.assertRaises(AssertionError): self.run_probe()
unittest.main(argv=['readiness-protocol'])
`
