import importlib.util
import pathlib

root = pathlib.Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("source_claim_controls", pathlib.Path(__file__).with_name("source-claims-negative.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)
runner.OUTPUT = root / "scratchpad/node-v2/share-renderer-negative"
runner.OUTPUT.mkdir(parents=True, exist_ok=True)
template = "internal/util/sharelink/template.go"
adapter = "internal/cores/internal/xray/share.go"
runner.CASES = [
    ("explicit-endpoint", adapter, "template.Render(host, inst.Port,", "template.Render(inst.Listen, inst.Port,",
     "./internal/cores", "^TestCoreClientShareUsesExplicitEndpointAndOnlySelectedClient$", "explicit endpoint"),
    ("selected-credential", template, "uuid := core.CredString(user.Credentials, core.CredUUID)", 'uuid := "other-client-credential"',
     "./internal/cores", "^TestCoreClientShareUsesExplicitEndpointAndOnlySelectedClient$", "selected credential"),
    ("server-secret", template, 't.Params["type"] = t.Network', 't.Params["type"] = t.Network; t.Params["leak"] = inst.Settings',
     "./internal/cores", "^TestCoreClientShareUsesExplicitEndpointAndOnlySelectedClient$", "share exposed unrelated material"),
    ("compiler-uuid-alias", template, "\t\tuuid = alias\n", "",
     "./internal/cores", "^TestCoreClientShareHonoursCompilerUUIDAliasAndRefusesConflicts$", "compiler UUID alias lost"),
    ("uuid-conflict", template, 'if uuid != "" && uuid != alias {', 'if false {',
     "./internal/cores", "^TestCoreClientShareHonoursCompilerUUIDAliasAndRefusesConflicts$", "ambiguous credential accepted"),
    ("legacy-clash-delegation", "internal/sub/clash_share.go", 'if err != nil || share.Kind != "link" {\n\t\treturn nil, false\n\t}', 'if err != nil || share.Kind != "link" {\n\t\treturn nil, true\n\t}',
     "./internal/sub", "^TestBuildProxy_VLESSRealityFieldsForClash$", "buildProxy returned nil"),
    ("legacy-clash-representation", "internal/sub/clash_share.go", 'if proxy == nil {\n\t\treturn nil, false\n\t}', 'if proxy == nil {\n\t\treturn nil, true\n\t}',
     "./internal/sub", "^TestClashExternalShadowsocksMatchesInboundPath$", "want a proxy from both paths"),
]

runner.main()
