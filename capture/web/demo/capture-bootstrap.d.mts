export interface CaptureBootstrapConfiguration {
  readonly coreUrl?: string;
  readonly apiKey?: string;
  readonly region?: string;
  readonly launchCredential?: string;
  readonly controller?: string;
  readonly recipient?: string;
}

export interface CaptureBootstrapPlugin {
  readonly name: string;
  configureServer(server: {
    readonly middlewares: { use(path: string, handler: (...arguments_: any[]) => any): void };
    readonly config: { readonly logger: { error(message: string): void } };
  }): void;
}

export declare function createCaptureBootstrapPlugin(
  configuration: CaptureBootstrapConfiguration,
): CaptureBootstrapPlugin;
