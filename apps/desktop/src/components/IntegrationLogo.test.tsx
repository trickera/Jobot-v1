import { describe, expect, it } from "vitest";
import { integrationKeyForName, integrationLabel } from "./IntegrationLogo";

describe("IntegrationLogo", () => {
  it("recognizes the compact WeWorkRemotely value emitted by the backend", () => {
    expect(integrationKeyForName("WeWorkRemotely")).toBe("weworkremotely");
    expect(integrationLabel("WeWorkRemotely")).toBe("We Work Remotely");
  });

  it("keeps unknown integrations on the explicit fallback", () => {
    expect(integrationKeyForName("Custom Board")).toBe("other");
    expect(integrationLabel("Custom Board")).toBe("Custom Board");
  });
});
