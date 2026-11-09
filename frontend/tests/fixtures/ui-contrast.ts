import type { Page } from "@playwright/test";

// Canvas resolves the browser's CSS colors, including HeroUI's color-mix tokens.
export async function measureThemeContrast(page: Page) {
  return page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 1;
    const context = canvas.getContext("2d", { willReadFrequently: true })!;
    const probe = document.createElement("span");
    document.body.append(probe);

    function color(token: string) {
      probe.style.color = `var(${token})`;
      context.clearRect(0, 0, 1, 1);
      context.fillStyle = getComputedStyle(probe).color;
      context.fillRect(0, 0, 1, 1);
      return Array.from(
        context.getImageData(0, 0, 1, 1).data,
        (value) => value / 255,
      );
    }
    function composite(foreground: number[], background: number[]) {
      return foreground
        .slice(0, 3)
        .map(
          (channel, index) =>
            channel * foreground[3] + background[index] * (1 - foreground[3]),
        );
    }
    function luminance(channels: number[]) {
      const linear = channels
        .slice(0, 3)
        .map((channel) =>
          channel <= 0.04045
            ? channel / 12.92
            : ((channel + 0.055) / 1.055) ** 2.4,
        );
      return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722;
    }
    const pairs = [
      ["Text", "--foreground", "--background", 4.5],
      ["Secondary text", "--muted", "--surface", 4.5],
      ["Grouped secondary text", "--muted", "--surface-secondary", 4.5],
      ["Field text", "--field-foreground", "--field-background", 4.5],
      // HeroUI's default fields are borderless. Test the focus indicator for
      // those controls; test the border too when a bordered theme is active.
      ["Field focus", "--focus", "--field-background", 3],
      ...(parseFloat(
        getComputedStyle(probe).getPropertyValue("--field-border-width"),
      ) > 0
        ? [["Field border", "--field-border", "--field-background", 3] as const]
        : []),
      ["Primary action", "--accent-foreground", "--accent", 4.5],
      ["Error text", "--danger", "--surface", 4.5],
      ["Destructive action", "--danger-foreground", "--danger", 4.5],
      [
        "Destructive secondary action",
        "--danger-soft-foreground",
        "--danger-soft",
        4.5,
      ],
      ["Approved status", "--success-soft-foreground", "--success-soft", 4.5],
    ] as const;
    const surface = color("--surface");
    const measurements = pairs.map(
      ([name, foregroundToken, backgroundToken, minimum]) => {
        const background = composite(color(backgroundToken), surface);
        const foreground = composite(color(foregroundToken), background);
        const first = luminance(foreground);
        const second = luminance(background);
        return {
          name,
          minimum,
          ratio:
            (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05),
        };
      },
    );
    probe.remove();
    return measurements;
  });
}
