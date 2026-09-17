// The kit's published window contract, included by every module's typecheck.
import type { Contrast, FontPref, MenuConfig, MenuExtra, MenuJump } from "./menu-model";

declare global {
  interface Window {
    dopeMenu?: {
      setJump(jump: MenuJump | null): void;
      clearJump(): void;
      setExtras(items: MenuExtra[]): void;
      clearExtras(): void;
      openModal(): void;
      readonly theme: string;
      readonly contrast: Contrast;
      readonly font: FontPref;
      setFont(font: string): void;
    };
    dopeMenuConfig?: MenuConfig;
  }
}

export {};
