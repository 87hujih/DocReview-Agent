/** @type {import('next').NextConfig} */
const nextConfig = {
  // standalone 输出模式与部署流程中的 Docker 镜像目录结构保持一致。
  output: "standalone"
};

export default nextConfig;
