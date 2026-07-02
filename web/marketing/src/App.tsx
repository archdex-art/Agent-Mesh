import { GradientMesh } from "./components/GradientMesh";
import { Nav } from "./components/Nav";
import { Hero } from "./components/Hero";
import { LogoBar } from "./components/LogoBar";
import { Features } from "./components/Features";
import { Stats } from "./components/Stats";
import { HowItWorks } from "./components/HowItWorks";
import { Compare } from "./components/Compare";
import { CTA } from "./components/CTA";
import { Footer } from "./components/Footer";

function App() {
  return (
    <div className="relative min-h-screen bg-[#05050a]">
      <div className="absolute inset-x-0 top-0 h-[900px]">
        <GradientMesh />
      </div>
      <Nav />
      <main className="relative">
        <Hero />
        <LogoBar />
        <Features />
        <Stats />
        <HowItWorks />
        <Compare />
        <CTA />
      </main>
      <Footer />
    </div>
  );
}

export default App;
